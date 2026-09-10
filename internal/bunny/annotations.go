package bunny

import (
	"strconv"

	"sigs.k8s.io/external-dns/endpoint"
)

const (
	providerSpecificDisabled    = "webhook/bunny-disabled"
	providerSpecificMonitorType = "webhook/bunny-monitor-type"
	providerSpecificWeight      = "webhook/bunny-weight"
)

type providerSpecificOptions struct {
	Disabled    bool
	MonitorType MonitorType
	Weight      int
}

func providerSpecificOptionsFromEndpoint(e *endpoint.Endpoint) (providerSpecificOptions, error) {
	opts := providerSpecificOptions{}

	if disabled, ok := e.GetProviderSpecificProperty(providerSpecificDisabled); ok {
		var err error
		opts.Disabled, err = strconv.ParseBool(disabled)
		if err != nil {
			opts.Disabled = false
		}
	}

	if monitorType, ok := e.GetProviderSpecificProperty(providerSpecificMonitorType); ok {
		opts.MonitorType = MonitorTypeFromString(monitorType)
	}

	if weight, ok := e.GetProviderSpecificProperty(providerSpecificWeight); ok {
		var err error
		opts.Weight, err = strconv.Atoi(weight)
		if err != nil {
			opts.Weight = 100
		}

		if opts.Weight < 1 {
			opts.Weight = 1
		}

		if opts.Weight > 100 {
			opts.Weight = 100
		}
	}

	if opts.Weight == 0 {
		opts.Weight = 100
	}

	return opts, nil
}

func providerSpecificOptionsFromRecord(r *Record) *providerSpecificOptions {
	opts := &providerSpecificOptions{
		MonitorType: r.MonitorType,
		Weight:      r.Weight,
		Disabled:    r.Disabled,
	}

	return opts
}

func (p *providerSpecificOptions) ApplyToEndpoint(e *endpoint.Endpoint, recordType RecordType) {
	// Don't apply default values, so external-dns doesn't try to reconcile them
	// back to non-existence.
	// XXX: this does imply that an attempt to actually set the defaults with
	// annotations will then be reconciled constantly back into existence.
	if p.MonitorType != MonitorTypeNone {
		e.WithProviderSpecific(providerSpecificMonitorType, p.MonitorType.String())
	}
	// HACK: some record types don't support weight, in which case it's zero.
	if recordType != RecordTypeSRV && p.Weight != 0 && p.Weight != 100 {
		e.WithProviderSpecific(providerSpecificWeight, strconv.Itoa(p.Weight))
	}
	if p.Disabled {
		e.WithProviderSpecific(providerSpecificDisabled, strconv.FormatBool(p.Disabled))
	}
}
