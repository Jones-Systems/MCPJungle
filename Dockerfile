FROM gcr.io/distroless/base

# OCI image labels
LABEL org.opencontainers.image.source="https://github.com/mcpjungle/mcpjungle"
LABEL org.opencontainers.image.description="MCPJungle - Self-hosted MCP Gateway for developers and enterprises"
LABEL org.opencontainers.image.title="MCPJungle"
LABEL org.opencontainers.image.vendor="mcpjungle"

# The build is handled by goreleaser
# Copy the binary from the build stage
COPY mcpjungle /mcpjungle

EXPOSE 8080
ENTRYPOINT ["/mcpjungle"]

# Bind inside the container so published ports and peer containers can reach the server.
CMD ["start", "--host", "0.0.0.0"]