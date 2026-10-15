// Package notify 负责 Telegram 机器人通知的发送与编辑。
// 凭据与代理等全局参数来自 internal/config 的进程级配置 config.C。
package notify

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"oci-help/internal/config"
	"oci-help/internal/proxy"
)

// Message 是 Telegram Bot API 的响应体。
type Message struct {
	OK          bool `json:"ok"`
	Result      `json:"result"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

// Result 是响应中的有效负载。
type Result struct {
	MessageId int `json:"message_id"`
}

// SendMessage 发送一条通知；未配置 Telegram 时静默跳过。
// name 是消息标题前缀，text 为正文（Markdown）。
func SendMessage(name, text string) (Message, error) {
	return send(config.C.SendURL, name, text, -1)
}

// EditMessage 编辑已发送的通知；messageId 为要修改的消息 ID。
func EditMessage(messageId int, name, text string) (Message, error) {
	return send(config.C.EditURL, name, text, messageId)
}

func send(apiURL, name, text string, messageId int) (msg Message, err error) {
	if config.C == nil || config.C.Token == "" || config.C.ChatID == "" {
		return msg, nil
	}
	data := url.Values{
		"parse_mode": {"Markdown"},
		"chat_id":    {config.C.ChatID},
		"text":       {"🔰*甲骨文通知* " + name + "\n" + text},
	}
	if messageId > 0 {
		data.Set("message_id", strconv.Itoa(messageId))
	}
	var req *http.Request
	req, err = http.NewRequest(http.MethodPost, apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Transport: proxy.Transport(config.C.Proxy)}
	var resp *http.Response
	resp, err = client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var body []byte
	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}
	err = json.Unmarshal(body, &msg)
	if err != nil {
		return
	}
	if !msg.OK {
		err = errors.New(msg.Description)
	}
	return
}
