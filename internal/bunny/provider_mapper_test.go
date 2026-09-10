package bunny

import (
	"context"
	"reflect"
	"testing"

	"github.com/puzpuzpuz/xsync/v3"
	"sigs.k8s.io/external-dns/endpoint"
)

func TestRecordToEndpointFormatsMXAndSRVTargets(t *testing.T) {
	tests := []struct {
		name   string
		record Record
		target string
	}{
		{
			name: "MX",
			record: Record{
				Name:     "mail",
				Type:     RecordTypeMX,
				Priority: 10,
				Value:    "in1-smtp.messagingengine.com",
			},
			target: "10 in1-smtp.messagingengine.com",
		},
		{
			name: "SRV",
			record: Record{
				Name:     "_submission._tcp",
				Type:     RecordTypeSRV,
				Priority: 0,
				Weight:   1,
				Port:     587,
				Value:    "smtp.fastmail.com",
			},
			target: "0 1 587 smtp.fastmail.com",
		},
		{
			name: "disabled SRV",
			record: Record{
				Name:     "_caldav._tcp",
				Type:     RecordTypeSRV,
				Priority: 0,
				Weight:   0,
				Port:     0,
				Value:    disabledSRVTarget,
				Disabled: true,
			},
			target: "0 0 0 ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recordToEndpoint("example.com", &tt.record)
			if !reflect.DeepEqual(got.Targets, endpoint.Targets{tt.target}) {
				t.Fatalf("recordToEndpoint() targets = %v, want [%q]", got.Targets, tt.target)
			}

			if tt.name == "disabled SRV" {
				if _, ok := got.GetProviderSpecificProperty(providerSpecificDisabled); ok {
					t.Fatal("disabled SRV exposed a provider-specific disabled annotation")
				}
			}
		})
	}
}

func TestCreateEndpointsConvertsEveryMXAndSRVTarget(t *testing.T) {
	client := &recordingClient{}
	provider := &Provider{
		client:  client,
		zoneMap: xsync.NewMapOf[string, int64](),
	}
	provider.zoneMap.Store("example.com", 1)

	endpoints := []*endpoint.Endpoint{
		endpoint.NewEndpointWithTTL(
			"example.com",
			"MX",
			300,
			"10 in1-smtp.messagingengine.com.",
			"20 in2-smtp.messagingengine.com.",
		),
		endpoint.NewEndpointWithTTL(
			"_submission._tcp.example.com",
			"SRV",
			300,
			"0 1 587 smtp.fastmail.com.",
			"0 0 0 .",
		),
	}

	if err := provider.createEndpoints(context.Background(), endpoints); err != nil {
		t.Fatalf("createEndpoints() error = %v", err)
	}

	want := []CreateRecordRequest{
		{
			Name:       "",
			Type:       RecordTypeMX,
			TTLSeconds: 300,
			Value:      "in1-smtp.messagingengine.com",
			Priority:   10,
			Weight:     100,
		},
		{
			Name:       "",
			Type:       RecordTypeMX,
			TTLSeconds: 300,
			Value:      "in2-smtp.messagingengine.com",
			Priority:   20,
			Weight:     100,
		},
		{
			Name:       "_submission._tcp",
			Type:       RecordTypeSRV,
			TTLSeconds: 300,
			Value:      "smtp.fastmail.com",
			Priority:   0,
			Weight:     1,
			Port:       587,
		},
		{
			Name:       "_submission._tcp",
			Type:       RecordTypeSRV,
			TTLSeconds: 300,
			Value:      disabledSRVTarget,
			Priority:   0,
			Weight:     0,
			Port:       0,
			Disabled:   true,
		},
	}

	if !reflect.DeepEqual(client.created, want) {
		t.Fatalf("created records = %#v, want %#v", client.created, want)
	}
}

func TestSetRecordTargetRejectsInvalidRecordTargets(t *testing.T) {
	tests := []struct {
		recordType RecordType
		target     string
	}{
		{recordType: RecordTypeMX, target: "mail.example.com"},
		{recordType: RecordTypeSRV, target: "0 1"},
		{recordType: RecordTypeSRV, target: "priority 1 443 service.example.com"},
	}

	for _, tt := range tests {
		if err := setRecordTarget(&Record{Type: tt.recordType}, tt.target); err == nil {
			t.Errorf("setRecordTarget(%s, %q) succeeded", tt.recordType.String(), tt.target)
		}
	}
}

type recordingClient struct {
	created []CreateRecordRequest
}

func (c *recordingClient) ListZones(context.Context, ListZonesRequest) (*ListZonesResponse, error) {
	return nil, nil
}

func (c *recordingClient) CreateRecord(_ context.Context, _ string, request CreateRecordRequest) (*Record, error) {
	c.created = append(c.created, request)
	return &Record{}, nil
}

func (c *recordingClient) UpdateRecord(context.Context, int64, int64, UpdateRecordRequest) error {
	return nil
}

func (c *recordingClient) DeleteRecord(context.Context, int64, int64) error {
	return nil
}
