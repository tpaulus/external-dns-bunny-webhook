package bunny

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"strconv"
	"strings"

	"github.com/puzpuzpuz/xsync/v3"
	"github.com/samber/oops"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"
)

var (
	_ provider.Provider = (*Provider)(nil)
)

type Options struct {
	APIKey               string   `env:"API_KEY, required"`
	DryRun               bool     `env:"DRY_RUN, default=false"`
	ExcludeDomains       []string `env:"EXCLUDE_DOMAINS"`
	ExcludeDomainsRegexp string   `env:"EXCLUDE_DOMAINS_REGEXP"`
	IncludeDomains       []string `env:"INCLUDE_DOMAINS"`
	IncludeDomainsRegexp string   `env:"INCLUDE_DOMAINS_REGEXP"`
}

type Provider struct {
	Options Options
	client  Client
	filter  endpoint.DomainFilterInterface
	zoneMap *xsync.MapOf[string, int64]
}

type zoneDomainFilter struct {
	zones      endpoint.DomainFilterInterface
	configured endpoint.DomainFilterInterface
}

func (f zoneDomainFilter) Match(domain string) bool {
	return f.zones.Match(domain) && f.configured.Match(domain)
}

func NewProvider(client Client, options Options) *Provider {
	provider := &Provider{
		Options: options,
		client:  client,
		filter:  getDomainFilter(options),
		zoneMap: xsync.NewMapOf[string, int64](),
	}

	// On startup, fetch zones so that all available zones are cached. This
	// is necessary to avoid making a call to the API during creates as we
	// need the zone ID to create a record. In addition, this data is used
	// to accurately exctract recordName from the full dnsName. Without it,
	// we could not accurately handle all the expected TLDs without maintaing
	// an internal list.
	_, err := provider.fetchZones(context.Background())
	if err != nil {
		slog.Error("Failed to fetch zones on startup.",
			slog.Any("error", err))
	}

	return provider
}

func (p *Provider) allZones() []string {
	var zones []string

	p.zoneMap.Range(func(key string, value int64) bool {
		zones = append(zones, key)
		return true
	})

	return zones
}

func (p *Provider) cacheZone(zone *Zone) {
	p.zoneMap.Store(zone.Domain, zone.ID)
}

func (p *Provider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	errs := oops.In("Provider").
		Span("Records")

	zones, err := p.fetchZones(ctx)
	if err != nil {
		slog.Error("Failed to fetch zones",
			slog.Any("error", err))

		return nil, errs.Wrapf(err, "failed to fetch zones")
	}

	var endpoints []*endpoint.Endpoint
	endpointIndexes := make(map[endpoint.EndpointKey]int)
	for _, zone := range zones {
		for _, record := range zone.Records {
			// First check if the record type is supported, and if not
			// skip the record altogether.
			if !p.SupportedRecordType(record.Type.String()) {
				continue
			}

			ep := recordToEndpoint(zone.Domain, record)
			key := ep.Key()
			if index, ok := endpointIndexes[key]; ok {
				endpoints[index].Targets = append(endpoints[index].Targets, ep.Targets...)
				continue
			}

			endpointIndexes[key] = len(endpoints)
			endpoints = append(endpoints, ep)
		}
	}

	return endpoints, nil
}

func (p *Provider) SupportedRecordType(recordType string) bool {
	switch recordType {
	case "A", "AAAA", "CNAME", "MX", "SRV", "TXT":
		return true
	default:
		return false
	}
}

func (p *Provider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {

	if changes == nil || !changes.HasChanges() {
		slog.Debug("Skipping request to apply changes because no changes are present")
		return nil
	}

	errs := oops.In("Provider").Span("ApplyChanges")

	var createCount, deleteCount, updateCount int
	if changes != nil {
		createCount = len(changes.Create)
		deleteCount = len(changes.Delete)
		updateCount = len(changes.UpdateNew)
	}

	errs = errs.With("creates", createCount).
		With("deletes", deleteCount).
		With("updates", updateCount)

	if p.Options.DryRun {
		return p.applyChangesDryRun(ctx, changes)
	}

	deletions := append(append([]*endpoint.Endpoint{}, changes.Delete...), changes.UpdateOld...)
	if len(deletions) > 0 {
		tuples, err := p.fetchIdentifiers(ctx, deletions)
		if err != nil {
			slog.Error("Failed to fetch identifiers",
				slog.Any("error", err))

			return errs.Wrapf(err, "failed to fetch identifiers")
		}

		if err := p.deleteEndpoints(ctx, tuples, deletions); err != nil {
			slog.Error("Failed to delete endpoints",
				slog.Any("error", err))

			return errs.Wrapf(err, "failed to apply deletes")
		}
	}

	creates := append(append([]*endpoint.Endpoint{}, changes.Create...), changes.UpdateNew...)
	if err := p.createEndpoints(ctx, creates); err != nil {
		slog.Error("Failed to create endpoints",
			slog.Any("error", err))

		return errs.Wrapf(err, "failed to apply creates")
	}

	return nil
}

func (p *Provider) applyChangesDryRun(ctx context.Context, changes *plan.Changes) error {
	if changes == nil || !changes.HasChanges() {
		slog.Debug("DRY RUN: Skipping request to apply changes because no changes are present")
		return nil
	}

	errs := oops.In("Provider").Span("ApplyChanges")

	var createCount, deleteCount, updateCount int
	if changes != nil {
		createCount = len(changes.Create)
		deleteCount = len(changes.Delete)
		updateCount = len(changes.UpdateNew)
	}

	errs = errs.With("creates", createCount).
		With("deletes", deleteCount).
		With("updates", updateCount)

	for _, ep := range changes.Create {
		slog.InfoContext(ctx, "DRY RUN: Create record",
			slog.Group("record",
				slog.Any("name", ep.DNSName),
				slog.Any("type", ep.RecordType),
				slog.Any("value", ep.Targets),
				slog.Any("ttl", ep.RecordTTL),
			))
	}

	// If we have no deletions or updates, we can return early to avoid making a (potentially)
	// expensive call to the Bunny.net API.
	if len(changes.Delete) == 0 && len(changes.UpdateOld) == 0 {
		return nil
	}

	deletions := append(append([]*endpoint.Endpoint{}, changes.Delete...), changes.UpdateOld...)
	tuples, err := p.fetchIdentifiers(ctx, deletions)
	if err != nil {
		slog.Error("Failed to fetch identifiers",
			slog.Any("error", err))

		return errs.Wrapf(err, "failed to fetch identifiers")
	}

	for _, ep := range changes.Delete {
		for _, target := range ep.Targets {
			identifierTuples, ok := tuples[[3]string{ep.RecordType, ep.DNSName, target}]
			if !ok || len(identifierTuples) == 0 {
				slog.InfoContext(ctx, "DRY RUN: Delete record (would skip, not found in Bunny API)",
					slog.Group("record",
						slog.String("name", ep.DNSName),
						slog.String("type", ep.RecordType),
						slog.String("value", target),
						slog.Int("ttl", int(ep.RecordTTL)),
					))

				continue
			}
			tuple := identifierTuples[0]

			slog.InfoContext(ctx, "DRY RUN: Delete record",
				slog.Int64("zone_id", tuple.ZoneID),
				slog.Group("record",
					slog.Int64("id", tuple.RecordID),
					slog.String("name", ep.DNSName),
					slog.String("type", ep.RecordType),
					slog.String("value", target),
					slog.Int("ttl", int(ep.RecordTTL)),
				))
		}
	}

	for _, ep := range changes.UpdateOld {
		for _, target := range ep.Targets {
			identifierTuples, ok := tuples[[3]string{ep.RecordType, ep.DNSName, target}]
			if !ok || len(identifierTuples) == 0 {
				slog.InfoContext(ctx, "DRY RUN: Update record (would skip, not found in Bunny API)",
					slog.Group("record",
						slog.String("name", ep.DNSName),
						slog.String("type", ep.RecordType),
						slog.String("value", target),
						slog.Int("ttl", int(ep.RecordTTL)),
					))

				continue
			}
			tuple := identifierTuples[0]

			slog.InfoContext(ctx, "DRY RUN: Replace record",
				slog.Int64("zone_id", tuple.ZoneID),
				slog.Group("record",
					slog.Int64("id", tuple.RecordID),
					slog.String("name", ep.DNSName),
					slog.String("type", ep.RecordType),
					slog.String("value", target),
					slog.Int("ttl", int(ep.RecordTTL)),
				))
		}
	}

	return nil
}

// AdjustEndpoints canonicalizes a set of candidate endpoints.
// It is called with a set of candidate endpoints obtained from the various sources.
// It returns a set modified as required by the provider. The provider is responsible for
// adding, removing, and modifying the ProviderSpecific properties to match
// the endpoints that the provider returns in `Records` so that the change plan will not have
// unnecessary (potentially failing) changes. It may also modify other fields, add, or remove
// Endpoints. It is permitted to modify the supplied endpoints.
func (p *Provider) AdjustEndpoints(incoming []*endpoint.Endpoint) ([]*endpoint.Endpoint, error) {
	errs := oops.In("Provider").
		Span("AdjustEndpoints")

	fetched, err := p.Records(context.Background())
	if err != nil {
		slog.Error("Failed to fetch records",
			slog.Any("error", err))

		return nil, errs.Wrapf(err, "failed to fetch records")
	}

	for _, editing := range incoming {
		normalizeEndpointTargets(editing)

		for _, checked := range fetched {
			if editing.DNSName != checked.DNSName || editing.RecordType != checked.RecordType || editing.SetIdentifier != checked.SetIdentifier {
				continue
			}

			maps.Copy(editing.Labels, checked.Labels)
		}
	}

	return incoming, nil
}

func normalizeEndpointTargets(ep *endpoint.Endpoint) {
	switch ep.RecordType {
	case endpoint.RecordTypeCNAME, endpoint.RecordTypeMX, endpoint.RecordTypeSRV:
		for index, target := range ep.Targets {
			ep.Targets[index] = strings.TrimSuffix(target, ".")
		}
	}
}

// GetDomainFilter returns the domain filter used by this provider.
func (p *Provider) GetDomainFilter() endpoint.DomainFilterInterface {
	var zones []string
	p.zoneMap.Range(func(zone string, _ int64) bool {
		if p.filter.Match(zone) {
			zones = append(zones, zone)
		}
		return true
	})

	// Keep the configured filter when the initial zone lookup failed. Records
	// will then be retried once the Bunny API is reachable again.
	if len(zones) == 0 {
		return p.filter
	}

	return zoneDomainFilter{
		zones:      endpoint.NewDomainFilter(zones),
		configured: p.filter,
	}
}

// createEndpoints creates the given endpoints.
func (p *Provider) createEndpoints(ctx context.Context, creates []*endpoint.Endpoint) error {
	errs := oops.In("Provider").
		Span("createEndpoints").
		With("creates", len(creates))

	for _, create := range creates {
		recordName, domainName, ok := extractRecordComponents(p.allZones(), create.DNSName)
		if !ok {
			slog.Warn("Skipping record for domain not hosted by Bunny.",
				slog.String("dns_name", create.DNSName))
			continue
		}

		bunnyZoneID, ok := p.zoneMap.Load(domainName)
		if !ok {
			return errs.Errorf("zone ID for DNS name %q (%s) not found", create.DNSName, domainName)
		}

		opts, err := providerSpecificOptionsFromEndpoint(create)
		if err != nil {
			return errs.Wrapf(err, "failed to create record %q", create.DNSName)
		}

		for _, target := range create.Targets {
			record := Record{
				Name:        recordName,
				Type:        RecordTypeFromString(create.RecordType),
				TTLSeconds:  int(create.RecordTTL),
				MonitorType: opts.MonitorType,
				Weight:      opts.Weight,
				Disabled:    opts.Disabled,
			}
			if err := setRecordTarget(&record, target); err != nil {
				return errs.Wrapf(err, "failed to parse record %q", create.DNSName)
			}

			request := CreateRecordRequest{
				Name:        record.Name,
				Type:        record.Type,
				TTLSeconds:  record.TTLSeconds,
				Value:       record.Value,
				Priority:    record.Priority,
				Port:        record.Port,
				MonitorType: record.MonitorType,
				Weight:      record.Weight,
				Disabled:    record.Disabled,
			}

			slog.Debug("Creating Record.",
				slog.String("zone", domainName),
				slog.Int64("zone_id", bunnyZoneID),
				slog.Group("record",
					slog.String("name", request.Name),
					slog.String("type", request.Type.String()),
					slog.String("value", request.Value),
					slog.Int("ttl", request.TTLSeconds),
					slog.Int("priority", request.Priority),
					slog.Int("port", request.Port),
					slog.Int("weight", request.Weight),
					slog.Bool("disabled", request.Disabled),
				),
			)

			created, err := p.client.CreateRecord(ctx, strconv.FormatInt(bunnyZoneID, 10), request)
			if err != nil {
				slog.Error("Failed to create record.",
					slog.Any("error", err),
					slog.Group("record",
						slog.String("name", request.Name),
						slog.String("type", request.Type.String()),
						slog.String("value", request.Value),
						slog.Int("ttl", request.TTLSeconds),
						slog.Int("priority", request.Priority),
						slog.Int("port", request.Port),
						slog.Int("weight", request.Weight),
						slog.Bool("disabled", request.Disabled),
					))

				return err
			}

			slog.InfoContext(ctx, "Record created successfully.",
				slog.String("zone", domainName),
				slog.Int64("zone_id", bunnyZoneID),
				slog.Group("record",
					slog.Int64("id", created.ID),
					slog.String("name", request.Name),
					slog.String("type", request.Type.String()),
					slog.String("value", request.Value),
					slog.Int("ttl", request.TTLSeconds),
					slog.Int("priority", request.Priority),
					slog.Int("port", request.Port),
					slog.Int("weight", request.Weight),
					slog.Bool("disabled", request.Disabled),
				))
		}
	}

	return nil
}

func (p *Provider) deleteEndpoints(ctx context.Context, identifiers map[[3]string][]identifierTuple, deletions []*endpoint.Endpoint) error {
	for _, deletion := range deletions {
		for _, target := range deletion.Targets {
			key := [3]string{deletion.RecordType, deletion.DNSName, target}
			tuples, ok := identifiers[key]
			if !ok {
				return fmt.Errorf("failed to get record identifiers for %q target %q", deletion.DNSName, target)
			}
			if len(tuples) == 0 {
				return fmt.Errorf("no record identifiers remain for %q target %q", deletion.DNSName, target)
			}

			tuple := tuples[0]
			identifiers[key] = tuples[1:]

			if err := p.client.DeleteRecord(ctx, tuple.ZoneID, tuple.RecordID); err != nil {
				return err
			}

			slog.InfoContext(ctx, "Deleted record.",
				slog.Int64("zone_id", tuple.ZoneID),
				slog.Group("record",
					slog.Int64("id", tuple.RecordID),
					slog.String("name", deletion.DNSName),
					slog.String("value", target),
					slog.Int("ttl", int(deletion.RecordTTL)),
				))
		}

	}

	return nil
}

type identifierTuple struct {
	ZoneID   int64
	RecordID int64
}

// fetchIdentifiers fetches the zone and record identifiers for the given endpoints. Keeping all
// identifiers for each target lets an update delete pre-existing duplicate Bunny records.
func (p *Provider) fetchIdentifiers(ctx context.Context, endpoints []*endpoint.Endpoint) (map[[3]string][]identifierTuple, error) {
	identifiers := make(map[[3]string][]identifierTuple)

	zones, err := p.fetchZones(ctx)
	if err != nil {
		return nil, err
	}

	var domainNames []string
	for _, zone := range zones {
		domainNames = append(domainNames, zone.Domain)
	}

	for _, ep := range endpoints {
		recordName, domainName, ok := extractRecordComponents(domainNames, ep.DNSName)
		if !ok {
			return nil, fmt.Errorf("record %q %q cannot be handled, no matching zone found", ep.RecordType, ep.DNSName)
		}

		for _, zone := range zones {
			if zone.Domain != domainName {
				continue
			}

			for _, record := range zone.Records {
				if record.Name != recordName || record.Type.String() != ep.RecordType {
					continue
				}

				target := targetFromRecord(record)
				for _, endpointTarget := range ep.Targets {
					if strings.TrimSuffix(target, ".") != strings.TrimSuffix(endpointTarget, ".") {
						continue
					}

					key := [3]string{ep.RecordType, ep.DNSName, endpointTarget}
					identifiers[key] = append(identifiers[key], identifierTuple{
						ZoneID:   zone.ID,
						RecordID: record.ID,
					})
					break
				}
			}
		}
	}

	return identifiers, nil
}

func (p *Provider) fetchZones(ctx context.Context) ([]*Zone, error) {
	var page = 1
	var zones []*Zone

	for {
		results, err := p.client.ListZones(ctx, ListZonesRequest{
			Page:    page,
			PerPage: 1000,
		})

		if err != nil {
			return nil, err
		}

		for _, zone := range results.Items {
			// Cache the zone ID for lookup during creates.
			p.cacheZone(zone)

			zones = append(zones, zone)
		}

		if !results.HasMoreItems {
			break
		}

		page++
	}

	return zones, nil
}

// Match the given `dnsName` to one of the `zones`.
//
// The first return result is the record prefix, which may be the empty string
// if dnsName matches a zone exactly (i.e. a root record).
//
// The second return result is the matched zone.
//
// The third return result denotes whether the search was successful.
func extractRecordComponents(zones []string, dnsName string) (string, string, bool) {
	for _, zone := range zones {
		if dnsName == zone {
			return "", zone, true
		} else if strings.HasSuffix(dnsName, "."+zone) {
			return strings.TrimSuffix(dnsName, "."+zone), zone, true
		}
	}

	return "", "", false
}

func getDomainFilter(options Options) endpoint.DomainFilterInterface {
	if options.ExcludeDomainsRegexp != "" || options.IncludeDomainsRegexp != "" {
		return endpoint.NewRegexDomainFilter(
			regexp.MustCompile(options.IncludeDomainsRegexp),
			regexp.MustCompile(options.ExcludeDomainsRegexp),
		)
	}

	return endpoint.NewDomainFilterWithExclusions(options.IncludeDomains, options.ExcludeDomains)
}
