package main

import (
	"encoding/json"
	"expvar"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/gorilla/websocket"
)

var (
	token = os.Getenv("SLACK_TOKEN")

	ignored = expvar.NewInt("ignored")
	regular = expvar.NewInt("regular")
	changed = expvar.NewInt("changed")
	matched = expvar.NewInt("matched")

	regexes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bwow\b`),
		regexp.MustCompile(`(?i)\bamaze\b`),
		regexp.MustCompile(`(?i)\bexcite\b`),
		regexp.MustCompile(`(?i)\bdoge\b`),
	}
)

var ping = make(chan struct{}, 10)

func init() {
	go func() {
		for {
			time.Sleep(10 * time.Minute)
			log.Printf("ignored: %s, regular: %s, changed: %s, matched: %s",
				ignored.String(), regular.String(), changed.String(), matched.String())
			ping <- struct{}{}
		}
	}()
}

func start() (wsUrl string, err error) {
	body, err := slack("rtm.start")
	if err != nil {
		return "", err
	}
	var v = struct {
		URL string `json:"url"`
	}{}
	json.Unmarshal(body, &v)
	return v.URL, nil
}

func doge(channel, timestamp string) error {
	_, err := slack("reactions.add",
		"name", "doge",
		"channel", channel,
		"timestamp", timestamp,
	)
	return err
}

func main() {
	wsUrl, err := start()
	if err != nil {
		log.Fatal(err)
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsUrl, http.Header{})
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	go func() {
		for {
			<-ping
			err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second))
			if err != nil {
				log.Fatal(err)
			}
		}
	}()
	for {
		_, b, err := conn.ReadMessage()
		if err != nil {
			conn.Close()
			log.Fatalf("failed on websocket read: %s", err.Error())
		}

		var v = struct {
			Type string `json:"type"`
		}{}
		err = json.Unmarshal(b, &v)
		if err != nil {
			log.Print(err)
			continue
		}
		switch v.Type {
		case "hello":
			log.Print("rtm hello received")
		case "message":
			go message(b)
		}
	}
}

func message(body []byte) {
	var v = struct {
		Channel   string `json:"channel"`
		Timestamp string `json:"ts"`
		Text      string `json:"text"`
		Subtype   string `json:"subtype"`
		Message   struct {
			Text      string `json:"text"`
			Timestamp string `json:"ts"`
		} `json:"message"`
	}{}
	err := json.Unmarshal(body, &v)
	if err != nil {
		log.Print(err)
		return
	}
	text, timestamp := v.Text, v.Timestamp
	switch v.Subtype {
	case "message_changed":
		text, timestamp = v.Message.Text, v.Message.Timestamp
		changed.Add(1)
	case "":
		regular.Add(1)
	default:
		ignored.Add(1)
		return
	}
	match := false
	for _, re := range regexes {
		if re.Match([]byte(text)) {
			match = true
			break
		}
	}
	if !match {
		return
	}
	matched.Add(1)
	err = doge(v.Channel, timestamp)
	if err == slackError("already_reacted") {
		return
	}
	if err != nil {
		log.Printf("unable to doge %s %s: %s", v.Channel, timestamp, err.Error())
	}

}

type slackError string

func (e slackError) Error() string {
	return "slack api response contained error: " + string(e)
}

func slack(method string, arguments ...string) (body []byte, err error) {
	log.Print(method, " ", arguments)
	q := url.Values{"token": {token}}
	for i := 0; i < len(arguments); i += 2 {
		if i+1 < len(arguments) {
			q.Add(arguments[i], arguments[i+1])
		} else {
			q.Add(arguments[i], "")
		}
	}
	resp, err := http.PostForm("https://slack.com/api/"+method, q)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return body, err
	}
	var okerr struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	err = json.Unmarshal(body, &okerr)
	if !okerr.OK {
		return body, slackError(okerr.Error)
	}
	return body, err
}
