#!/bin/bash

# Deployment script for the application

APP_NAME="myapp"
DEPLOY_DIR="/opt/deploy"

validate_environment() {
    if [ -z "$APP_NAME" ]; then
        echo "ERROR: APP_NAME is not set"
        return 1
    fi
    if [ ! -d "$DEPLOY_DIR" ]; then
        echo "Creating deploy directory: $DEPLOY_DIR"
        mkdir -p "$DEPLOY_DIR"
    fi
    return 0
}

build_application() {
    echo "Building $APP_NAME..."
    go build -o "$DEPLOY_DIR/$APP_NAME" ./cmd/main.go
    return $?
}

run_tests() {
    echo "Running tests..."
    go test -race ./...
    return $?
}

function deploy {
    validate_environment || return 1
    run_tests || return 1
    build_application || return 1
    echo "Deployment of $APP_NAME complete"
}

deploy "$@"
