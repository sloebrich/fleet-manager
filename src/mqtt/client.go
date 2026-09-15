package transport

import (
	"fmt"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type Client struct {
	client paho.Client
}

func New(clientID string, brokerURL string) (*Client, error) {
	opts := paho.NewClientOptions()
	opts.SetClientID(clientID)
	opts.AddBroker(brokerURL)

	client := paho.NewClient(opts)

	token := client.Connect()
	if token.Wait() && token.Error() != nil {
		return nil, fmt.Errorf("connect to MQTT broker %w", token.Error())
	}

	return &Client{
		client: client,
	}, nil

}

func (c *Client) Publish(topic string, payload any) error {
	token := c.client.Publish(topic, 1, false, payload)

	if token.Wait() && token.Error() != nil {
		return token.Error()
	}

	return nil
}
func (c *Client) Subscribe(topic string, handler paho.MessageHandler) error {
	token := c.client.Subscribe(topic, 1, handler)

	if token.Wait() && token.Error() != nil {
		return token.Error()
	}

	return nil
}

func (c *Client) Close() {
	c.client.Disconnect(1000)
}
