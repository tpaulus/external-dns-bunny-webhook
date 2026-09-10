# Kubernetes ExternalDNS - bunny.net provider

[![Main](https://github.com/tpaulus/external-dns-bunny-webhook/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/tpaulus/external-dns-bunny-webhook/actions/workflows/ci.yaml)

![Alt](https://repobeats.axiom.co/api/embed/84439b4fb9d24956578d7e6f92953965a005b6bb.svg "Repobeats analytics image")

[ExternalDNS](https://github.com/kubernetes-sigs/external-dns) synchronises
exposed Kubernetes Services and Ingresses with DNS providers.

This repository contains an ExternalDNS provider for [bunny.net](https://bunny.net).


## Important

This provider is not officially supported by Bunny.net, but is maintained by [Chris Grimmett](https://grimtech.net). This provider is working in production for my use at https://confettihat.com, so I want to keep it running smoothly for myself and the community. If you encounter any issues, please open an issue or discussion on this repository. 

This is an fork of <https://nossa.ee/~talya/external-dns-bunny-webhook>, without nix.

## Deployment

See examples directory for an example of deploying the provider with kluctl/Helm.

Configuration options are available below and may be set using environment
variables on the webhook container.

## Supported record types

The provider supports A, AAAA, CNAME, MX, SRV, and TXT records. MX and SRV
targets use ExternalDNS's standard format: `<priority> <hostname>` for MX and
`<priority> <weight> <port> <hostname>` for SRV. An SRV target of `.` is
represented as a disabled Bunny record, preserving its RFC 2782
service-unavailable behavior.


## Configuration

The provider can be configured using the following environment variables:

| Environment Variable    | Required | Description                                                                  | Default     |
|-------------------------|----------|------------------------------------------------------------------------------|-------------|
| `BUNNY_API_KEY`         | Yes      | The API key used to authenticate with the Bunny.net API.                     |             |
| `BUNNY_DRY_RUN`         | No       | If set to `true`, the provider will not make any changes to the DNS records. | `false`     |
| `WEBHOOK_HOST`          | No       | The host to use for the webhook endpoint.                                    | `localhost` |
| `WEBHOOK_PORT`          | No       | The port to use for the webhook endpoint.                                    | `8888`      |
| `WEBHOOK_READ_TIMEOUT`  | No       | The read timeout for the webhook endpoint.                                   | `60s`       |
| `WEBHOOK_WRITE_TIMEOUT` | No       | The write timeout for the webhook endpoint.                                  | `60s`       |
| `HEALTH_HOST`           | No       | The host to use for the health endpoint.                                     | `0.0.0.0`   |
| `HEALTH_PORT`           | No       | The port to use for the health endpoint.                                     | `8080`      |
| `HEALTH_READ_TIMEOUT`   | No       | The read timeout for the health endpoint.                                    | `60s`       |
| `HEALTH_WRITE_TIMEOUT`  | No       | The write timeout for the health endpoint.                                   | `60s`       |


## Provider-Specific Annotations

The following annotations may be added to sources to control behavior of the DNS
records created by this provider:

### `external-dns.alpha.kubernetes.io/webhook-bunny-disabled`

If set to `true`, the DNS record will be managed but set to disabled in the
Bunny API. This annotation is optional and will default to `false` if not
provided. Disabling a record will cause it to not respond to DNS queries, but
will still be managed by the provider and visible in the Bunny.net dashboard.


### `external-dns.alpha.kubernetes.io/webhook-bunny-monitor-type`

The monitor type to use for the DNS record. Valid values are `none` (default),
`http`, and `ping`. This annotation is optional and will default to `none` if
not provided, which will create a standard DNS record without any monitoring.


### `external-dns.alpha.kubernetes.io/webhook-bunny-weight`

The weight to use for the DNS record. Valid values are between 1 and 100. This
annotation is optional and will default to `100` if not provided. Any value
outside of the valid range will be set to the nearest valid value, and any
non-integer value will result in the default value being used.


## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
