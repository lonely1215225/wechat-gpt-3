FROM golang:1.17.12
WORKDIR /app
COPY . .
CMD ["go","run","main.go"]