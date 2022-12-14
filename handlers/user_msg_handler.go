package handlers

import (
	"errors"
	"fmt"
	"github.com/869413421/wechatbot/config"
	"github.com/869413421/wechatbot/gpt"
	"github.com/869413421/wechatbot/pkg/logger"
	"github.com/869413421/wechatbot/service"
	"github.com/eatmoreapple/openwechat"
	"strings"
	"time"
)

var _ MessageHandlerInterface = (*UserMessageHandler)(nil)

// UserMessageHandler 私聊消息处理
type UserMessageHandler struct {
	// 接收到消息
	msg *openwechat.Message
	// 发送的用户
	sender *openwechat.User
	// 实现的用户业务
	service service.UserServiceInterface
}

var userCount uint

func (h *UserMessageHandler) LimitGPT() error {

	userCount++

	// 获取现在
	now := time.Now()
	// 定义当天结束时间
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local)
	// 计算开始到结束剩下多少时间
	left := end.Sub(now).Hours()

	if left < 0 {
		userCount = 0
	}

	if config.LoadConfig().PrivateChatLimitCount < userCount {
		return errors.New("user limited")
	}

	return nil
}

func UserMessageContextHandler() func(ctx *openwechat.MessageContext) {
	return func(ctx *openwechat.MessageContext) {
		msg := ctx.Message
		handler, err := NewUserMessageHandler(msg)
		if err != nil {
			logger.Warning(fmt.Sprintf("init user message handler error: %s", err))
		}

		// 处理用户消息
		err = handler.handle()
		if err != nil {
			logger.Warning(fmt.Sprintf("handle user message error: %s", err))
		}
	}
}

// NewUserMessageHandler 创建私聊处理器
func NewUserMessageHandler(message *openwechat.Message) (MessageHandlerInterface, error) {
	sender, err := message.Sender()
	if err != nil {
		return nil, err
	}
	userService := service.NewUserService(c, sender)
	handler := &UserMessageHandler{
		msg:     message,
		sender:  sender,
		service: userService,
	}

	return handler, nil
}

// handle 处理消息
func (h *UserMessageHandler) handle() error {
	triggerKeyword := config.LoadConfig().ChatPrivateTriggerKeyword
	logger.Info(h.msg.Content, triggerKeyword)
	if h.msg.Content == triggerKeyword {
		h.msg.ReplyText("  你好 我是有时智能有时智障的GPT智能机器人 是否智障取决于你问我的问题 对于已知的事情我知道的很多 我可以用这些已知的事情帮你创作内容 \n  由于我是语言模型并非网络模型 所以我也会和人一样不知道今天的天气等等 \n  目前可以自定义私聊触发关键词、机器人回复的温度（更有创造性还是更明确答案的）、清空上下文记忆、私聊回复前缀、ai模型、自动通过好友、每日回答限制数 \n后期会根据情况上线一些问答小游戏、连接网络去获取实时信息、图片视频等内容发送等功能 敬请期待！！！")
		return nil
	}
	user, err := h.sender.Self.Bot.GetCurrentUser()
	logger.Info(err)
	if h.msg.IsText() && h.sender.ID() != user.ID() {
		content := h.msg.Content

		prefixIndex := strings.Index(content, triggerKeyword)
		lastIndex := strings.LastIndex(content, triggerKeyword)
		if triggerKeyword != "" && (prefixIndex == -1 || lastIndex == -1) {
			return nil
		}
		return h.ReplyText()
	}
	return nil
}

// ReplyText 发送文本消息到群
func (h *UserMessageHandler) ReplyText() error {
	logger.Info(fmt.Sprintf("Received User %v Text Msg : %v", h.sender.NickName, h.msg.Content))

	errLimit := h.LimitGPT()
	if errLimit != nil {
		h.msg.ReplyText("出于安全与成本的考虑 GPT已超过今日最大使用次数 请联系管理员进行配置")
		return errors.New("超过今日最大使用次数 请联系管理员进行配置")
	}

	// 1.获取上下文，如果字符串为空不处理
	requestText := h.getRequestText()
	if requestText == "" {
		logger.Info("user message is null")
		return nil
	}

	// 2.向GPT发起请求，如果回复文本等于空,不回复
	reply, err := gpt.Completions(h.getRequestText())
	if err != nil {
		// 2.1 将GPT请求失败信息输出给用户，省得整天来问又不知道日志在哪里。
		errMsg := fmt.Sprintf("gpt request error: %v", err)
		_, err = h.msg.ReplyText(errMsg)
		if err != nil {
			return errors.New(fmt.Sprintf("response user error: %v ", err))
		}
		return err
	}

	// 2.设置上下文，回复用户
	h.service.SetUserSessionContext(requestText, reply)
	_, err = h.msg.ReplyText(buildUserReply(reply))
	if err != nil {
		return errors.New(fmt.Sprintf("response user error: %v ", err))
	}

	// 3.返回错误
	return err
}

// getRequestText 获取请求接口的文本，要做一些清晰
func (h *UserMessageHandler) getRequestText() string {
	// 1.去除空格以及换行
	requestText := strings.TrimSpace(h.msg.Content)
	requestText = strings.Trim(h.msg.Content, "\n")
	requestText = strings.Replace(requestText, config.LoadConfig().ChatPrivateTriggerKeyword, "", 1)

	// 2.获取上下文，拼接在一起，如果字符长度超出4000，截取为4000。（GPT按字符长度算），达芬奇3最大为4068，也许后续为了适应要动态进行判断。
	sessionText := h.service.GetUserSessionContext()
	if sessionText != "" {
		requestText = sessionText + "\n" + requestText
	}
	if len(requestText) >= 4000 {
		requestText = requestText[:4000]
	}

	// 3.检查用户发送文本是否包含结束标点符号
	punctuation := ",.;!?，。！？、…"
	runeRequestText := []rune(requestText)
	lastChar := string(runeRequestText[len(runeRequestText)-1:])
	if strings.Index(punctuation, lastChar) < 0 {
		requestText = requestText + "？" // 判断最后字符是否加了标点，没有的话加上句号，避免openai自动补齐引起混乱。
	}

	// 4.返回请求文本
	return requestText
}

// buildUserReply 构建用户回复
func buildUserReply(reply string) string {
	// 1.去除空格问号以及换行号，如果为空，返回一个默认值提醒用户
	textSplit := strings.Split(reply, "\n\n")
	if len(textSplit) > 1 {
		trimText := textSplit[0]
		reply = strings.Trim(reply, trimText)
	}
	reply = strings.TrimSpace(reply)

	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "请求得不到任何有意义的回复，请具体提出问题。"
	}

	// 2.如果用户有配置前缀，加上前缀
	reply = config.LoadConfig().ReplyPrefix + "\n" + reply
	reply = strings.Trim(reply, "\n")

	// 3.返回拼接好的字符串
	return reply
}
