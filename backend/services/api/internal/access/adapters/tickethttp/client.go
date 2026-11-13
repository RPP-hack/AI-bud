package tickethttp

import (
	"ai-bud/services/api/internal/access/domain"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var ErrUnavailable = errors.New("ticket service unavailable")

type CreateInput struct {
	OperationID string `json:"operation_id"`
	OwnerID     string `json:"owner_id"`
	Resource    string `json:"resource"`
	Access      string `json:"access"`
	Reason      string `json:"reason"`
}
type Result struct {
	ID     string              `json:"id"`
	Status domain.TicketStatus `json:"status"`
}
type Client struct {
	baseURL, token string
	http           *http.Client
}

func New(baseURL, token string, client *http.Client) *Client {
	return &Client{strings.TrimRight(baseURL, "/"), token, client}
}
func (c *Client) Create(ctx context.Context, key, owner string, p domain.Parameters) (domain.Ticket, error) {
	data, err := json.Marshal(CreateInput{key, owner, p.Resource, p.Access, p.Reason})
	if err != nil {
		return domain.Ticket{}, err
	}
	return c.call(ctx, http.MethodPost, "/tickets", data)
}
func (c *Client) Lookup(ctx context.Context, key string) (domain.Ticket, error) {
	return c.call(ctx, http.MethodGet, "/tickets/"+url.PathEscape(key), nil)
}
func (c *Client) call(ctx context.Context, method, path string, data []byte) (domain.Ticket, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return domain.Ticket{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return domain.Ticket{}, domain.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return domain.Ticket{}, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	var out Result
	dec := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&out); err != nil {
		return domain.Ticket{}, fmt.Errorf("%w: invalid response", ErrUnavailable)
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return domain.Ticket{}, ErrUnavailable
	}
	t := domain.Ticket{ID: out.ID, Status: out.Status}
	if err = t.Validate(); err != nil {
		return domain.Ticket{}, fmt.Errorf("%w: invalid ticket", ErrUnavailable)
	}
	return t, nil
}
