// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type mailAddress struct {
	Address string `json:"Address"`
}

type mailSummary struct {
	ID string        `json:"ID"`
	To []mailAddress `json:"To"`
}

// mailToken reads only this disposable project's sink. Mail content and tokens
// stay in memory and are never included in errors or failure artifacts.
func (s *Stack) mailToken(ctx context.Context, email, parameter string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var listing struct {
			Messages []mailSummary `json:"messages"`
		}
		if err := sinkJSON(ctx, s.MailURL+"/api/v1/messages", &listing); err == nil {
			for _, message := range listing.Messages {
				matches := false
				for _, recipient := range message.To {
					matches = matches || strings.EqualFold(recipient.Address, email)
				}
				if !matches {
					continue
				}
				var body struct {
					Text string `json:"Text"`
					HTML string `json:"HTML"`
				}
				if err := sinkJSON(ctx, s.MailURL+"/api/v1/message/"+url.PathEscape(message.ID), &body); err == nil {
					if token := tokenFromMail(body.Text+"\n"+body.HTML, parameter); token != "" {
						return token, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", errors.New("disposable mail delivery did not provide the expected server-issued token before deadline")
		case <-ticker.C:
		}
	}
}

func tokenFromMail(body, parameter string) string {
	links := regexp.MustCompile(`https?://[^\s<>"']+`).FindAllString(html.UnescapeString(body), -1)
	for _, link := range links {
		parsed, err := url.Parse(link)
		if err == nil && parsed.Query().Get(parameter) != "" {
			return parsed.Query().Get(parameter)
		}
	}
	return ""
}

func sinkJSON(ctx context.Context, endpoint string, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("construct disposable sink request")
	}
	response, err := disposableHTTPClient().Do(request)
	if err != nil {
		return errors.New("disposable sink request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return errors.New("disposable sink returned non-success status")
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(result); err != nil {
		return errors.New("disposable sink returned invalid JSON")
	}
	return nil
}
