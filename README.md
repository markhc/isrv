# isrv

Simple anonymous and temporary file sharing service.

Visit https://isrv.nl to see it in action.

[![Go Build](https://github.com/markhc/isrv/actions/workflows/build.yaml/badge.svg)](https://github.com/markhc/isrv/actions/workflows/build.yaml)

## Description

isrv is a lightweight file sharing service that provides anonymous temporary storage with customizable expiration times. Users can upload files and share them via generated links without requiring registration or personal information.

## Goals

- Anonymous temporary storage, with customizable expiration time
- Easy installation: Single statically-linked binary that can be deployed anywhere
- Manage your own uploads, without compromising anonymity

## TODO

This project is a work in progress, here's a list of things I am working on in no particular order:

- Implement remote storage options (S3, GCS)
- Allow users to manage their uploads (return a token on upload that can be used to delete/manage the file)
- Compress files at rest to save storage when convenient (text and other highly compressible formats)
- Optional file encryption at rest

## Installation

### Pre-built binaries

Download the latest release for your platform from the releases page and make it executable:

```bash
# Linux
wget https://github.com/markhc/isrv/releases/latest/download/isrv-linux-amd64
chmod +x isrv-linux-amd64
sudo mv isrv-linux-amd64 /usr/local/bin/isrv
```

### Docker

```bash
docker run -p 8080:8080 ghcr.io/markhc/isrv:latest
```

### From source

Requires Go 1.25.5 or later:

```bash
git clone https://github.com/markhc/isrv.git
cd isrv
make build
```

The binary will be available in the `build/` directory.

## Usage

Running the server is as easy as starting the binary.

```bash
# Generates a default configuration file on $HOME/.config/isrv/config.yaml
isrv --makeconf

# Starts the webserver (will load config file if it exists)
isrv

# Starts the webserver with a specific config file
isrv -c config.yaml
```

If no config file is provided the application will look for one in standard places and, if none can be found, default values will be used.

The web interface will be available at `http://localhost:8080`.

## Configuration

Configuration can be provided via:
- Environment variables
- Configuration file

## Development

### Building

Build for current platform:
```bash
make build
```

### Testing

Run tests:
```bash
make test
```

Run tests with coverage:
```bash
make test-coverage
```

### Development workflow

Format, lint, test, and build:
```bash
make dev
```

## License

See LICENSE file for details.
