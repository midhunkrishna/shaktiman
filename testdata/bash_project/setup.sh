#!/bin/bash

# Environment setup script

install_dependencies() {
    echo "Installing dependencies..."
    apt-get update && apt-get install -y curl git
}

configure_database() {
    local db_host="${DB_HOST:-localhost}"
    local db_port="${DB_PORT:-5432}"
    echo "Configuring database at $db_host:$db_port"
}

create_user() {
    local username="$1"
    local group="$2"
    useradd -m -g "$group" "$username"
    echo "Created user: $username"
}

setup_environment() {
    install_dependencies
    configure_database
    echo "Environment setup complete"
}
