#!/bin/bash

# Script to build a local test image for AltMount
# This uses the multi-stage Dockerfile which builds both frontend and backend

echo "🚀 Building AltMount test image..."

# Check if docker is installed
if ! command -v docker &> /dev/null; then
    echo "❌ Error: docker is not installed."
    exit 1
fi

# Build the image
# We use the root directory as context and the dev Dockerfile
docker build -t altmount:test -f docker/Dockerfile .

if [ $? -eq 0 ]; then
    echo "✅ Success! Test image 'altmount:test' created."
    echo ""
    echo "To run it:"
    echo "docker run -p 8080:8080 -v ./config:/config -v ./metadata:/metadata altmount:test"
else
    echo "❌ Error: Docker build failed."
    exit 1
fi
