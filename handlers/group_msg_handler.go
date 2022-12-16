package handlers

import (
	"errors"
	"fmt"
	"github.com/869413421/wechatbot/config"
	"github.com/869413421/wechatbot/gpt"
	"github.com/869413421/wechatbot/pkg/logger"
	"github.com/869413421/wechatbot/service"
	"github.com/eatmoreapple/openwechat"
	"path"
	"strings"
	"time"
)

var _ MessageHandlerInterface = (*GroupMessageHandler)(nil)

// GroupMessageHandler 群消息处理
type GroupMessageHandler struct {
	// 获取自己
	self *openwechat.Self
	// 群
	group *openwechat.Group
	// 接收到消息
	msg *openwechat.Message
	// 发送的用户
	sender *openwechat.User
	// 实现的用户业务
	service service.UserServiceInterface
}

// 被群里@次数
var groupCount uint

func (g *GroupMessageHandler) LimitGPT() error {

	groupCount++

	// 获取现在
	now := time.Now()
	// 定义当天结束时间
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local)
	// 计算开始到结束剩下多少时间
	left := end.Sub(now).Hours()

	if left < 0 {
		groupCount = 0
	}

	if config.LoadConfig().GroupChatLimitCount < groupCount {
		return errors.New("group limited")
	}

	return nil
}

func GroupMessageContextHandler() func(ctx *openwechat.MessageContext) {
	return func(ctx *openwechat.MessageContext) {
		msg := ctx.Message
		// 获取用户消息处理器
		handler, err := NewGroupMessageHandler(msg)
		if err != nil {
			logger.Warning(fmt.Sprintf("init group message handler error: %s", err))
			return
		}
		// 处理用户消息
		err = handler.handle()
		if err != nil {
			logger.Warning(fmt.Sprintf("handle group message error: %s", err))
		}
	}
}

// NewGroupMessageHandler 创建群消息处理器
func NewGroupMessageHandler(msg *openwechat.Message) (MessageHandlerInterface, error) {
	if msg.IsJoinGroup() && config.LoadConfig().WelcomeToGroup {
		content := msg.Content
		split := strings.Split(strings.Split(content, "邀请")[1], "加入了群聊")
		logger.Info(strings.Split(strings.Split(content, "邀请")[1], "加入了群聊"))
		msg.ReplyText(path.Join("让我们热烈欢迎👏🏻", split[0], "加入了群聊"))
		return nil, errors.New("invite success")
	}
	sender, err := msg.Sender()
	if err != nil {
		return nil, err
	}
	group := &openwechat.Group{User: sender}
	groupSender, err := msg.SenderInGroup()
	if err != nil {
		return nil, err
	}

	userService := service.NewUserService(c, groupSender)
	handler := &GroupMessageHandler{
		self:    sender.Self,
		msg:     msg,
		group:   group,
		sender:  groupSender,
		service: userService,
	}
	return handler, nil

}

// handle 处理消息
func (g *GroupMessageHandler) handle() error {
	if g.msg.IsText() {
		return g.ReplyText()
	}
	return nil
}

// ReplyText 发送文本消息到群
func (g *GroupMessageHandler) ReplyText() error {
	logger.Info(fmt.Sprintf("Received Group %v %v Text Msg : %v", g.group.NickName, g.sender.NickName, g.msg.Content))
	// 1.不是@的不处理
	if !g.msg.IsAt() {
		return nil
	}
	errLimit := g.LimitGPT()
	if errLimit != nil {
		g.msg.ReplyText("为了您的安全与费用，GPT已超过今日最大使用次数 请联系管理员进行配置")
		return errors.New("超过今日最大使用次数 请联系管理员进行配置")
	}
	// 2.获取请求的文本，如果为空字符串不处理
	requestText := g.getRequestText()
	if requestText == "" {
		logger.Info("user message is null")
		return nil
	}

	// 3.请求GPT获取回复
	reply, err := gpt.Completions(requestText)
	if err != nil {
		// 2.1 将GPT请求失败信息输出给用户，省得整天来问又不知道日志在哪里。
		errMsg := fmt.Sprintf("gpt request error: %v", err)
		_, err = g.msg.ReplyText(errMsg)
		if err != nil {
			return errors.New(fmt.Sprintf("response group error: %v ", err))
		}
		return err
	}

	// 4.设置上下文，并响应信息给用户
	g.service.SetUserSessionContext(requestText, reply)
	_, err = g.msg.ReplyText(g.buildReplyText(reply))
	if err != nil {
		return errors.New(fmt.Sprintf("response user error: %v ", err))
	}

	// 5.返回错误信息
	return err
}

// getRequestText 获取请求接口的文本，要做一些清洗
func (g *GroupMessageHandler) getRequestText() string {
	// 1.去除空格以及换行
	requestText := strings.TrimSpace(g.msg.Content)
	requestText = strings.Trim(g.msg.Content, "\n")

	// 2.替换掉当前用户名称
	replaceText := "@" + g.self.NickName
	requestText = strings.TrimSpace(strings.ReplaceAll(g.msg.Content, replaceText, ""))
	if requestText == "" {
		return ""
	}

	// 3.获取上下文，拼接在一起，如果字符长度超出4000，截取为4000。（GPT按字符长度算），达芬奇3最大为4068，也许后续为了适应要动态进行判断。
	sessionText := g.service.GetUserSessionContext()
	if sessionText != "" {
		requestText = sessionText + "\n" + requestText
	}
	if len(requestText) >= 4000 {
		requestText = requestText[:4000]
	}

	// 4.检查用户发送文本是否包含结束标点符号
	punctuation := ",.;!?，。！？、…"
	runeRequestText := []rune(requestText)
	lastChar := string(runeRequestText[len(runeRequestText)-1:])
	if strings.Index(punctuation, lastChar) < 0 {
		requestText = requestText + "？" // 判断最后字符是否加了标点，没有的话加上句号，避免openai自动补齐引起混乱。
	}

	// 5.返回请求文本
	return requestText
}

// buildReply 构建回复文本
func (g *GroupMessageHandler) buildReplyText(reply string) string {
	// 1.获取@我的用户
	atText := "@" + g.sender.NickName
	textSplit := strings.Split(reply, "\n\n")
	if len(textSplit) > 1 {
		trimText := textSplit[0]
		reply = strings.Trim(reply, trimText)
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return atText + " 请求得不到任何有意义的回复，请具体提出问题。"
	}

	// 2.拼接回复,@我的用户，问题，回复
	replaceText := "@" + g.self.NickName
	question := strings.TrimSpace(strings.ReplaceAll(g.msg.Content, replaceText, ""))
	reply = atText + "\n" + question + "\n --------------------------------\n" + reply
	reply = strings.Trim(reply, "\n")

	// 3.返回回复的内容
	return reply
}
