package bunny

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/samber/oops"
)

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client interface {
	ListZones(ctx context.Context, r ListZonesRequest) (*ListZonesResponse, error)
	CreateRecord(ctx context.Context, zoneID string, r CreateRecordRequest) (*Record, error)
	UpdateRecord(ctx context.Context, zoneID int64, recordID int64, r UpdateRecordRequest) error
	DeleteRecord(ctx context.Context, zoneID int64, recordID int64) error
}

type BunnyClient struct {
	client HTTPDoer
	apiKey string
}

func NewDNSClient(
	doer HTTPDoer,
	apiKey string,
) Client {
	return &BunnyClient{
		client: doer,
		apiKey: apiKey,
	}
}

type ListZonesRequest struct {
	Page    int    // Page number
	PerPage int    // Number of records per page
	Domain  string // Filter by domain
}

type ListZonesResponse struct {
	Items        []*Zone `json:"Items"`
	CurrentPage  int     `json:"CurrentPage"`
	TotalItems   int     `json:"TotalItems"`
	HasMoreItems bool    `json:"HasMoreItems"`
}

func closeResponseBody(resp *http.Response, errs oops.OopsErrorBuilder) {
	if err := resp.Body.Close(); err != nil {
		_ = errs.Wrapf(err, "failed to close response body") // Ignore the error from Wrapf
		slog.Error("Error closing response body", slog.Any("error", err))
	}
}

func (c *BunnyClient) ListZones(ctx context.Context, r ListZonesRequest) (*ListZonesResponse, error) {
	if r.PerPage < 1 {
		r.PerPage = 1000
	}

	errs := oops.In("BunnyClient").
		With("page", r.Page).
		With("per_page", r.PerPage).
		With("domain", r.Domain).
		Span("ListZones")

	var qp = make(url.Values)
	qp.Set("page", strconv.Itoa(r.Page))
	qp.Set("perPage", strconv.Itoa(r.PerPage))

	if r.Domain != "" {
		qp.Set("search", r.Domain)
	}

	slog.DebugContext(ctx, "Fetching Zones from Bunny.net API", slog.Group("req",
		slog.Int("page", r.Page),
		slog.Int("perPage", r.PerPage),
		slog.String("search", r.Domain)))

	req, err := c.createRequest(ctx, http.MethodGet, "/dnszone", qp)
	if err != nil {
		return nil, errs.Wrapf(err, "failed to create request")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errs.Wrapf(err, "failed to execute request")
	}

	defer closeResponseBody(resp, errs)

	if resp.StatusCode != http.StatusOK {
		return nil, handleUnexpectedResponse(errs, resp)
	}

	var body ListZonesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, errs.Wrapf(err, "failed to decode response")
	}

	return &body, nil
}

type CreateRecordRequest struct {
	Type        RecordType  `json:"Type"`
	TTLSeconds  int         `json:"Ttl"`
	Value       string      `json:"Value"`
	Name        string      `json:"Name"`
	Priority    int         `json:"Priority"`
	Port        int         `json:"Port"`
	MonitorType MonitorType `json:"MonitorType"`
	Weight      int         `json:"Weight"`
	Disabled    bool        `json:"Disabled"`
}

func (c *BunnyClient) CreateRecord(ctx context.Context, zoneID string, r CreateRecordRequest) (*Record, error) {
	if r.TTLSeconds == 0 { // Default to 5 minutes, chosen because this is the default in the Bunny.net UI
		r.TTLSeconds = 5 * 60 // 5 minutes
	}

	errs := oops.In("BunnyClient").
		With("zone_id", zoneID).
		With("type", r.Type).
		With("ttl", r.TTLSeconds).
		With("value", r.Value).
		With("name", r.Name).
		With("monitor_type", r.MonitorType).
		With("weight", r.Weight).
		With("disabled", r.Disabled).
		Span("CreateRecord")

	req, err := c.createRequestWithBody(ctx, http.MethodPut, fmt.Sprintf("/dnszone/%s/records", zoneID), r)
	if err != nil {
		return nil, errs.Wrapf(err, "failed to create request")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errs.Wrapf(err, "failed to send request")
	}

	defer closeResponseBody(resp, errs)

	if resp.StatusCode != http.StatusCreated {
		return nil, handleUnexpectedResponse(errs, resp)
	}

	var body Record
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, errs.Wrapf(err, "failed to decode response")
	}

	return &body, nil
}

func (c *BunnyClient) DeleteRecord(ctx context.Context, zoneID int64, recordID int64) error {
	errs := oops.In("BunnyClient").
		With("zoneID", zoneID).
		With("recordID", recordID).
		Span("DeleteRecord")

	req, err := c.createRequestWithBody(ctx, http.MethodDelete, fmt.Sprintf("/dnszone/%d/records/%d", zoneID, recordID), nil)
	if err != nil {
		return errs.Wrapf(err, "failed to create request")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return errs.Wrapf(err, "failed to send request")
	}

	defer closeResponseBody(resp, errs)

	if resp.StatusCode != http.StatusNoContent {
		return handleUnexpectedResponse(errs, resp)
	}

	return nil
}

type UpdateRecordRequest struct {
	TTLSeconds  int         `json:"Ttl"`
	Value       string      `json:"Value"`
	Priority    int         `json:"Priority"`
	Port        int         `json:"Port"`
	MonitorType MonitorType `json:"MonitorType"`
	Weight      int         `json:"Weight"`
	Disabled    bool        `json:"Disabled"`
}

func (c *BunnyClient) UpdateRecord(ctx context.Context, zoneID int64, recordID int64, r UpdateRecordRequest) error {
	errs := oops.In("BunnyClient").
		With("zoneID", zoneID).
		With("recordID", recordID).
		With("updatedTTL", r.TTLSeconds).
		With("updatedValue", r.Value).
		With("updatedMonitorType", r.MonitorType).
		With("updatedWeight", r.Weight).
		With("updatedDisabled", r.Disabled).
		Span("UpdateRecord")

	req, err := c.createRequestWithBody(ctx, http.MethodPost, fmt.Sprintf("/dnszone/%d/records/%d", zoneID, recordID), r)
	if err != nil {
		return errs.Wrapf(err, "failed to create request")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return errs.Wrapf(err, "failed to send request")
	}

	defer closeResponseBody(resp, errs)

	if resp.StatusCode != http.StatusNoContent {
		return handleUnexpectedResponse(errs, resp)
	}

	return nil
}

func (c *BunnyClient) createRequest(ctx context.Context, method string, path string, query url.Values) (*http.Request, error) {
	url := &url.URL{
		Scheme:   "https",
		Host:     "api.bunny.net",
		Path:     path,
		RawQuery: query.Encode(),
	}

	req, err := http.NewRequestWithContext(ctx, method, url.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("AccessKey", c.apiKey)

	return req, nil
}

func (c *BunnyClient) createRequestWithBody(ctx context.Context, method string, path string, body any) (*http.Request, error) {
	url := &url.URL{
		Scheme: "https",
		Host:   "api.bunny.net",
		Path:   path,
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, fmt.Errorf("failed to encode request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, url.String(), &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("AccessKey", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	return req, nil
}

func handleUnexpectedResponse(errBuilder oops.OopsErrorBuilder, resp *http.Response) error {
	var errBody map[string]any

	//nolint:errcheck // This is already an error path, so we don't care about the error here.
	json.NewDecoder(resp.Body).Decode(&errBody)

	err := errBuilder.
		With("status", resp.Status).
		With("statusCode", resp.StatusCode).
		Errorf("unexpected status code: %d", resp.StatusCode)

	slog.Error("Received an unexpected response from Bunny.net.",
		slog.Any("error", err),
		slog.Group("res",
			slog.String("status", resp.Status),
			slog.Int("statusCode", resp.StatusCode),
			slog.Any("headers", resp.Header),
			slog.Any("body", errBody),
		),
	)

	return err
}
