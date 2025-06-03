#!/bin/bash

set -e

# Function to download and install nats CLI
install_nats_cli() {
  echo "Installing nats CLI..."
  NATS_CLI_VERSION=0.2.3
  curl -L "https://github.com/nats-io/natscli/releases/download/v${NATS_CLI_VERSION}/nats-${NATS_CLI_VERSION}-linux-amd64.zip" -o nats-cli.zip
  unzip nats-cli.zip
  sudo mv "nats-${NATS_CLI_VERSION}-linux-amd64/nats" /usr/local/bin/
  rm -rf "./nats-${NATS_CLI_VERSION}-linux-amd64"
  rm -f "nats-cli.zip"
  echo "nats CLI installed successfully!"
}

# Function to download and install nsc CLI
install_nsc_cli() {
  echo "Installing nsc CLI..."
  NSC_CLI_VERSION=2.11.0
  curl -L "https://github.com/nats-io/nsc/releases/download/v${NSC_CLI_VERSION}/nsc-linux-amd64.zip" -o nsc.zip
  unzip nsc.zip
  sudo mv nsc /usr/local/bin/
  rm -f nsc.zip
  echo "nsc CLI installed successfully!"
}

# Install both CLIs
install_nats_cli
install_nsc_cli

# Verify installation
echo "Verifying installation..."
nats --version
nsc --version
echo "Installation complete!"
