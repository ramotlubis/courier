package highconnection

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
)

var (
	sendURL      = "https://highpushfastapi-v2.hcnx.eu/api"
	maxMsgLength = 1500
)

func init() {
	courier.RegisterHandler(newHandler())
}

type handler struct {
	handlers.BaseHandler
}

func newHandler() courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(courier.ChannelType("HX"), "High Connection")}
}

// Initialize is called by the engine once everything is loaded
func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveMessage)
	s.AddHandlerRoute(h, http.MethodGet, "status", h.receiveStatus)
	return nil
}

type moForm struct {
	To          string `name:"TO"              validate:"required"`
	From        string `name:"FROM"            validate:"required"`
	Message     string `name:"MESSAGE"`
	ReceiveDate string `name:"RECEPTION_DATE"`
}

// receiveMessage is our HTTP handler function for incoming messages
func (h *handler) receiveMessage(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	form := &moForm{}
	err := handlers.DecodeAndValidateForm(form, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	date := time.Now()
	if form.ReceiveDate != "" {
		date, err = time.Parse("2006-01-02T15:04:05", form.ReceiveDate)
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}
	}

	// create our URN
	urn, err := handlers.StrictTelForCountry(form.From, channel.Country())
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	// build our Message
	msg := h.Backend().NewIncomingMsg(channel, urn, form.Message).WithReceivedOn(date.UTC())

	// and finally write our message
	return handlers.WriteMsgsAndResponse(ctx, h, []courier.Msg{msg}, w, r)
}

type statusForm struct {
	RetID  int64 `name:"ret_id" validate:"required"`
	Status int   `name:"status" validate:"required"`
}

var statusMapping = map[int]courier.MsgStatusValue{
	2:  courier.MsgFailed,
	4:  courier.MsgSent,
	6:  courier.MsgDelivered,
	11: courier.MsgFailed,
	12: courier.MsgFailed,
	13: courier.MsgFailed,
	14: courier.MsgFailed,
	15: courier.MsgFailed,
	16: courier.MsgFailed,
}

// receiveStatus is our HTTP handler function for status updates
func (h *handler) receiveStatus(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	form := &statusForm{}
	err := handlers.DecodeAndValidateForm(form, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	msgStatus, found := statusMapping[form.Status]
	if !found {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("unknown status '%d', must be one of 2, 4, 6, 11, 12, 13, 14, 15  or 16", form.Status))
	}

	// write our status
	status := h.Backend().NewMsgStatusForID(channel, courier.NewMsgID(form.RetID), msgStatus)
	return handlers.WriteMsgStatusAndResponse(ctx, h, channel, status, w, r)
}

// SendMsg sends the passed in message, returning any error
func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	username := msg.Channel().StringConfigForKey(courier.ConfigUsername, "")
	if username == "" {
		return nil, fmt.Errorf("no username set for HX channel")
	}

	password := msg.Channel().StringConfigForKey(courier.ConfigPassword, "")
	if password == "" {
		return nil, fmt.Errorf("no password set for HX channel")
	}

	callbackDomain := msg.Channel().CallbackDomain(h.Server().Config().Domain)
	statusURL := fmt.Sprintf("https://%s/c/hx/%s/status", callbackDomain, msg.Channel().UUID())
	receiveURL := fmt.Sprintf("https://%s/c/hx/%s/receive", callbackDomain, msg.Channel().UUID())

	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)
	parts := handlers.SplitMsg(handlers.GetTextAndAttachments(msg), maxMsgLength)
	for _, part := range parts {

		form := url.Values{
			"accountid":  []string{username},
			"password":   []string{password},
			"text":       []string{part},
			"to":         []string{msg.URN().Path()},
			"ret_id":     []string{msg.ID().String()},
			"datacoding": []string{"8"},
			"userdata":   []string{"textit"},
			"ret_url":    []string{statusURL},
			"ret_mo_url": []string{receiveURL},
		}

		msgURL, _ := url.Parse(sendURL)
		msgURL.RawQuery = form.Encode()

		req, _ := http.NewRequest(http.MethodPost, msgURL.String(), nil)
		rr, err := utils.MakeHTTPRequest(req)

		// record our status and log
		log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
		status.AddLog(log)
		if err != nil {
			return status, nil
		}

		status.SetStatus(courier.MsgWired)

	}

	return status, nil
}
