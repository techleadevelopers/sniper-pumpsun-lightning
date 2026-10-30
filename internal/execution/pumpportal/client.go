package pumpportal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func New(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = "https://pumpportal.fun/api/trade"
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

// Buy envia uma ordem de compra denominada em SOL.
// pool: "pump" (default), "pump-amm", "auto", etc.
func (c *Client) Buy(
	ctx context.Context,
	mint string,
	amountSOL float64,
	slippagePct float64,
	priorityFeeSOL float64,
	pool string,
) (string, error) {
	if pool == "" {
		pool = "pump"
	}
	return c.send(ctx, Request{
		Action:           "buy",
		Mint:             mint,
		Amount:           amountSOL,
		DenominatedInSOL: "true",
		Slippage:         slippagePct,
		PriorityFee:      priorityFeeSOL,
		Pool:             pool,
		SkipPreflight:    false, // simula antes de enviar (mais seguro)
		JitoOnly:         false,
	})
}

// Sell vende uma percentagem ou quantidade de tokens.
// amount pode ser "100%" ou um número de tokens.
// denominatedInSol fica "false" (tokens).
func (c *Client) Sell(
	ctx context.Context,
	mint string,
	amount any,
	slippagePct float64,
	priorityFeeSOL float64,
	pool string,
) (string, error) {
	if pool == "" {
		pool = "auto"
	}
	return c.send(ctx, Request{
		Action:           "sell",
		Mint:             mint,
		Amount:           amount,
		DenominatedInSOL: "false",
		Slippage:         slippagePct,
		PriorityFee:      priorityFeeSOL,
		Pool:             pool,
		SkipPreflight:    false,
		JitoOnly:         false,
	})
}

func (c *Client) send(ctx context.Context, req Request) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("pumpportal: marshal request: %w", err)
	}

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("pumpportal: invalid base URL: %w", err)
	}
	q := u.Query()
	q.Set("api-key", c.apiKey)
	u.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", &APIError{StatusCode: resp.StatusCode, Body: string(raw)}
	}

	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("pumpportal: invalid JSON response: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("pumpportal: %s", out.Error)
	}
	if out.Signature == "" {
		return "", fmt.Errorf("pumpportal: empty signature")
	}
	return out.Signature, nil
}