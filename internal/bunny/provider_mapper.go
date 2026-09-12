package bunny

import (
	"fmt"
	"strconv"
	"strings"

	"sigs.k8s.io/external-dns/endpoint"
)

const disabledSRVTarget = "external-dns-disabled.invalid"

func recordToEndpoint(domain string, record *Record) *endpoint.Endpoint {
	var dnsName string
	if record.Name == "" {
		dnsName = domain
	} else {
		dnsName = record.Name + "." + domain
	}

	ep := endpoint.NewEndpointWithTTL(
		dnsName,
		record.Type.String(),
		endpoint.TTL(record.TTLSeconds),
		targetFromRecord(record),
	)

	if record.Type == RecordTypeSRV && record.Disabled && record.Value == disabledSRVTarget {
		return ep
	}

	ps := providerSpecificOptionsFromRecord(record)
	ps.ApplyToEndpoint(ep, record.Type)

	return ep
}

func targetFromRecord(record *Record) string {
	switch record.Type {
	case RecordTypeMX:
		return fmt.Sprintf("%d %s", record.Priority, fullyQualifiedHostname(record.Value))
	case RecordTypeSRV:
		target := fullyQualifiedHostname(record.Value)
		if record.Disabled && record.Value == disabledSRVTarget {
			target = "."
		}

		return fmt.Sprintf("%d %d %d %s", record.Priority, record.Weight, record.Port, target)
	case RecordTypeTXT:
		return unquoteTXTValue(record.Value)
	default:
		return record.Value
	}
}

func unquoteTXTValue(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return value[1 : len(value)-1]
	}

	return value
}

func setRecordTarget(record *Record, target string) error {
	switch record.Type {
	case RecordTypeMX:
		fields, err := recordTargetFields("MX", target, 2)
		if err != nil {
			return err
		}

		record.Priority, err = parseRecordField("MX priority", fields[0])
		if err != nil {
			return err
		}
		record.Value = hostnameForBunny(fields[1])
	case RecordTypeSRV:
		fields, err := recordTargetFields("SRV", target, 4)
		if err != nil {
			return err
		}

		record.Priority, err = parseRecordField("SRV priority", fields[0])
		if err != nil {
			return err
		}
		record.Weight, err = parseRecordField("SRV weight", fields[1])
		if err != nil {
			return err
		}
		record.Port, err = parseRecordField("SRV port", fields[2])
		if err != nil {
			return err
		}

		if fields[3] == "." {
			// Bunny rejects "." as an SRV hostname. A disabled record retains
			// the RFC 2782 service-unavailable behavior without serving it.
			record.Disabled = true
			record.Value = disabledSRVTarget
			return nil
		}

		record.Value = hostnameForBunny(fields[3])
	default:
		record.Value = target
	}

	return nil
}

func recordTargetFields(recordType, target string, expected int) ([]string, error) {
	fields := strings.Fields(target)
	if recordType == "SRV" && len(fields) == expected-1 {
		// endpoint.Endpoint trims a trailing dot, leaving an SRV root target
		// ("0 0 0 .") as three fields.
		fields = append(fields, ".")
	}
	if len(fields) != expected {
		return nil, fmt.Errorf("%s record target %q must contain %d fields", recordType, target, expected)
	}

	return fields, nil
}

func parseRecordField(name, value string) (int, error) {
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, value, err)
	}

	return int(parsed), nil
}

func hostnameForBunny(hostname string) string {
	return strings.TrimSuffix(hostname, ".")
}

func fullyQualifiedHostname(hostname string) string {
	if hostname == "" || hostname == "." {
		return "."
	}

	return strings.TrimSuffix(hostname, ".") + "."
}
