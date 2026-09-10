package bunny

import (
	"context"
	"reflect"
	"testing"

	"github.com/puzpuzpuz/xsync/v3"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
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

func TestCreateEndpointsSkipsUnmanagedZone(t *testing.T) {
	client := &recordingClient{}
	provider := &Provider{
		client:  client,
		zoneMap: xsync.NewMapOf[string, int64](),
	}
	provider.zoneMap.Store("example.com", 1)

	endpoints := []*endpoint.Endpoint{
		endpoint.NewEndpoint("unmanaged.example.net", "A", "192.0.2.1"),
	}

	if err := provider.createEndpoints(context.Background(), endpoints); err != nil {
		t.Fatalf("createEndpoints() error = %v", err)
	}
	if len(client.created) != 0 {
		t.Fatalf("created %d records for an unmanaged zone", len(client.created))
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

func TestGetDomainFilterRestrictsRecordsToBunnyZones(t *testing.T) {
	provider := &Provider{
		filter:  endpoint.NewDomainFilterWithExclusions(nil, []string{"excluded.example.com"}),
		zoneMap: xsync.NewMapOf[string, int64](),
	}
	provider.zoneMap.Store("example.com", 1)
	provider.zoneMap.Store("excluded.example.com", 2)

	filter := provider.GetDomainFilter()
	if !filter.Match("mail.example.com") {
		t.Fatal("Bunny zone record did not match the domain filter")
	}
	if filter.Match("mail.excluded.example.com") {
		t.Fatal("excluded Bunny zone record matched the domain filter")
	}
	if filter.Match("mail.unmanaged.net") {
		t.Fatal("unmanaged zone record matched the domain filter")
	}
}

func TestSupportedRecordType(t *testing.T) {
	provider := &Provider{}

	for _, recordType := range []string{"A", "AAAA", "CNAME", "MX", "SRV", "TXT"} {
		if !provider.SupportedRecordType(recordType) {
			t.Errorf("SupportedRecordType(%q) = false, want true", recordType)
		}
	}

	for _, recordType := range []string{"CAA", "NS", "PTR"} {
		if provider.SupportedRecordType(recordType) {
			t.Errorf("SupportedRecordType(%q) = true, want false", recordType)
		}
	}
}

func TestRecordsIncludesMXAndSRV(t *testing.T) {
	client := &recordingClient{
		zones: []*Zone{
			{
				ID:     1,
				Domain: "example.com",
				Records: []*Record{
					{Type: RecordTypeMX, Name: "", Priority: 10, Value: "mail.example.com"},
					{Type: RecordTypeMX, Name: "", Priority: 20, Value: "backup-mail.example.com"},
					{Type: RecordTypeSRV, Name: "_submission._tcp", Priority: 0, Weight: 1, Port: 587, Value: "smtp.example.com"},
				},
			},
		},
	}
	provider := &Provider{
		client:  client,
		zoneMap: xsync.NewMapOf[string, int64](),
	}

	records, err := provider.Records(context.Background())
	if err != nil {
		t.Fatalf("Records() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("Records() returned %d records, want 2", len(records))
	}
	if records[0].RecordType != "MX" || records[1].RecordType != "SRV" {
		t.Fatalf("Records() types = %q, %q, want MX, SRV", records[0].RecordType, records[1].RecordType)
	}
	if !reflect.DeepEqual(records[0].Targets, endpoint.Targets{"10 mail.example.com", "20 backup-mail.example.com"}) {
		t.Fatalf("Records() MX targets = %v, want two MX targets", records[0].Targets)
	}
}

func TestDuplicateRecordTargetsScheduleAnUpdate(t *testing.T) {
	current := endpoint.NewEndpoint(
		"example.com",
		"MX",
		"10 mail.example.com",
		"10 mail.example.com",
		"20 backup-mail.example.com",
	)
	current.Labels = map[string]string{endpoint.OwnerLabelKey: "external-dns"}
	desired := endpoint.NewEndpoint(
		"example.com",
		"MX",
		"10 mail.example.com",
		"20 backup-mail.example.com",
	)

	changes := (&plan.Plan{
		Current:        []*endpoint.Endpoint{current},
		Desired:        []*endpoint.Endpoint{desired},
		Policies:       []plan.Policy{&plan.SyncPolicy{}},
		ManagedRecords: []string{"MX"},
		OwnerID:        "external-dns",
	}).Calculate().Changes

	if len(changes.UpdateOld) != 1 || len(changes.UpdateNew) != 1 {
		t.Fatalf("duplicate MX records produced changes %#v, want one update", changes)
	}
	if !reflect.DeepEqual(changes.UpdateOld[0].Targets, endpoint.Targets{"10 mail.example.com", "10 mail.example.com", "20 backup-mail.example.com"}) {
		t.Fatalf("UpdateOld targets = %v, want all duplicate targets", changes.UpdateOld[0].Targets)
	}
}

func TestDeleteEndpointsDeletesEveryDuplicateTarget(t *testing.T) {
	client := &recordingClient{
		zones: []*Zone{
			{
				ID:     1,
				Domain: "example.com",
				Records: []*Record{
					{ID: 10, Type: RecordTypeMX, Name: "", Priority: 10, Value: "mail.example.com"},
					{ID: 11, Type: RecordTypeMX, Name: "", Priority: 10, Value: "mail.example.com"},
				},
			},
		},
	}
	provider := &Provider{client: client, zoneMap: xsync.NewMapOf[string, int64]()}
	deletion := endpoint.NewEndpoint("example.com", "MX", "10 mail.example.com", "10 mail.example.com")

	identifiers, err := provider.fetchIdentifiers(context.Background(), []*endpoint.Endpoint{deletion})
	if err != nil {
		t.Fatalf("fetchIdentifiers() error = %v", err)
	}
	if err := provider.deleteEndpoints(context.Background(), identifiers, []*endpoint.Endpoint{deletion}); err != nil {
		t.Fatalf("deleteEndpoints() error = %v", err)
	}
	if !reflect.DeepEqual(client.deleted, []int64{10, 11}) {
		t.Fatalf("deleted record IDs = %v, want [10 11]", client.deleted)
	}
}

type recordingClient struct {
	created []CreateRecordRequest
	deleted []int64
	zones   []*Zone
}

func (c *recordingClient) ListZones(context.Context, ListZonesRequest) (*ListZonesResponse, error) {
	return &ListZonesResponse{Items: c.zones}, nil
}

func (c *recordingClient) CreateRecord(_ context.Context, _ string, request CreateRecordRequest) (*Record, error) {
	c.created = append(c.created, request)
	return &Record{}, nil
}

func (c *recordingClient) UpdateRecord(context.Context, int64, int64, UpdateRecordRequest) error {
	return nil
}

func (c *recordingClient) DeleteRecord(_ context.Context, _ int64, recordID int64) error {
	c.deleted = append(c.deleted, recordID)
	return nil
}
