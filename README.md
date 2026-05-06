# Hospitality PMS Integration

> Multi-tenant Property Management System integration for hotel PBX systems

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green?style=flat)](LICENSE)

## Overview

This service connects hotel Property Management Systems (PMS) to PBX systems, enabling automatic synchronization of guest check-ins, message waiting indicators, and room phone configuration.

### Supported PBX Providers

| Provider | Type | Status |
|----------|------|--------|
| **Bicom PBXware** | Asterisk-based (ARI + REST) | ✅ Implemented |
| **Zultys MX** | Webhook-based (Session Auth) | ✅ Implemented |

### Supported PMS Protocols

| Protocol | Description | Status |
|----------|-------------|--------|
| **Mitel SX-200** | ASCII-based with STX/ETX framing | ✅ Implemented |
| **FIAS/Fidelio** | Oracle hospitality standard | ✅ Implemented |
| **TigerTMS iLink** | HTTP REST API middleware | 📋 Documented |

### Features

- **Multi-PBX Support**: Pluggable PBX backends (Bicom, Zultys, more)
- **Multi-Tenant**: Support multiple hotels on a single deployment
- **Real-time Sync**: Guest check-in/out immediately updates phone extensions
- **Message Waiting**: Automatic MWI lamp control from PMS
- **Room Mapping**: Flexible room-to-extension mapping strategies
- **Wake-Up Calls**: Schedule via PBX API
- **Extension Management**: Update names, service plans
- **Voicemail Control**: Delete all messages on guest checkout
- **Webhook Support**: Receive PBX call events via HTTP
- **Observability**: Prometheus metrics and structured logging

## Quick Start

### Prerequisites

- Go 1.24+
- PostgreSQL 15+
- Bicom PBXware 7.2+ with ARI enabled and API key configured

### Installation

```bash
git clone https://github.com/sagostin/pbx-hospitality.git
cd pbx-hospitality
go mod download
go build ./cmd/bicom-hospitality
```

### Configuration

Copy the example configuration:

```bash
cp config/example.yaml config/config.yaml
```

Edit `config/config.yaml` with your tenant settings.

### Running

```bash
# With local PostgreSQL
docker compose up -d db
./bicom-hospitality

# Or with Docker
docker compose up -d
```

### Verify

```bash
curl http://localhost:8080/health
# {"status":"ok"}

curl http://localhost:8080/api/v1/tenants
# [{"id":"hotel-alpha","name":"Hotel Alpha","pms_connected":true,"ari_connected":true}]
```

## Architecture

```
┌─────────────┐     ┌─────────────────────┐     ┌─────────────┐
│  Mitel PMS  │────▶│                     │────▶│   Bicom     │
│  (TCP/23)   │     │   Hospitality       │     │   PBXware   │
└─────────────┘     │   Integration       │     │   (ARI+API) │
                    │                     │     └─────────────┘
┌─────────────┐     │  ┌───────────────┐  │
│  FIAS PMS   │────▶│  │ Event Router  │  │
│ (TCP/3722)  │     │  └───────────────┘  │
└─────────────┘     │                     │
                    │  ┌───────────────┐  │
                    │  │ Bicom Client  │  │
                    │  └───────────────┘  │
                    └─────────────────────┘
```

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Health check (includes DB status) |
| GET | `/metrics` | Prometheus metrics |
| GET | `/api/v1/tenants` | List all tenants |
| GET | `/api/v1/tenants/{id}` | Get tenant details |
| GET | `/api/v1/tenants/{id}/status` | Get tenant status |
| GET | `/api/v1/tenants/{id}/rooms` | List room mappings |
| POST | `/api/v1/tenants/{id}/rooms` | Create room mapping |
| GET | `/api/v1/tenants/{id}/sessions` | List active guest sessions |
| GET | `/api/v1/tenants/{id}/sessions/{room}` | Get session by room |
| GET | `/api/v1/tenants/{id}/events` | List recent PMS events |
| POST | `/api/v1/pbx/webhook/{tenant}` | PBX webhook receiver |

## Development

### Run Tests

```bash
go test ./... -v
```

### Project Structure

```
├── cmd/bicom-hospitality/   # Application entry point
├── internal/
│   ├── api/                 # REST API handlers
│   ├── config/              # Configuration loading
│   ├── db/                  # PostgreSQL repository (pgx)
│   ├── metrics/             # Prometheus metrics
│   ├── pbx/                 # PBX provider abstraction
│   │   ├── bicom/           # Bicom PBXware provider
│   │   └── zultys/          # Zultys MX provider
│   ├── pms/                 # PMS protocol adapters
│   │   ├── mitel/           # Mitel SX-200 protocol
│   │   ├── fias/            # FIAS/Fidelio protocol
│   │   └── tigertms/        # TigerTMS REST adapter
│   └── tenant/              # Tenant management
├── migrations/              # Database schema
├── config/                  # Configuration examples
└── docs/                    # Documentation
```

## Documentation

### Guides

- [Architecture Guide](docs/architecture.md) - System design and components
- [PBX Providers Guide](docs/pbx-providers.md) - Bicom, Zultys, adding new providers
- [Deployment Guide](docs/deployment.md) - Production deployment best practices
- [Protocol Reference](docs/protocols.md) - PMS protocol specifications

### API & Reference

- [API Reference](docs/api-reference.md) - REST API endpoints
- [Bicom API Reference](docs/bicom-api.md) - Bicom PBXware API details
- [TigerTMS Integration](docs/tigertms.md) - TigerTMS REST adapter

### Resources

- [PBXware API Postman Collection](docs/bicom/PBXwareAPI_Doc.postman_collection.json) - Interactive API tests
- [PBXware Setup Automation Collection](docs/bicom/MT%20Setup%20Automation%20Collection%202.postman_collection.json) - Provisioning automation
- [Future Considerations](docs/future-considerations.md) - Roadmap and ideas

## Contributing

Contributions are welcome. Please ensure tests pass (`go test ./...`) before submitting pull requests.

## License

MIT
