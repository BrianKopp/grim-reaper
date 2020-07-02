package notification

import (
	"errors"

	"github.com/rs/zerolog/log"
	"github.com/slack-go/slack"
)

// slackNotifier implements Notifier for slack
type slackNotifier struct {
	client      *slack.Client
	channel     string
	sendSuccess bool
}

// NewForSlack makes a new Notifier implemented by slack
func NewForSlack(sendSuccess bool, channel string, token string) Notifier {
	client := slack.New(token)

	if client == nil {
		panic(errors.New("error making slack client"))
	}

	return &slackNotifier{
		client,
		channel,
		sendSuccess,
	}
}

// Notify sends a notification indicating success or failure
func (m *slackNotifier) Notify(success bool, err error, nodes []string) error {
	if success && !m.sendSuccess {
		log.Info().Strs("nodes", nodes).Msg("skip sending notification since success")
		return nil
	}

	slackMsg := slack.MsgOptionText(makeMessageString(success, err, nodes), false)

	_, _, err := m.client.PostMessage(m.channel, slackMsg)
	if err != nil {
		log.Error().Err(err).Msg("error notifying slack of result")
		return err
	}

	log.Info().Msg("successfully notified slack of result")
	return nil
}
