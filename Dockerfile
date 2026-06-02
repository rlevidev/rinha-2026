# Stage 1: Builder
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go.mod and go.sum and download dependencies
COPY go.mod ./
# Ensure go.sum exists, even if empty, before go mod download
RUN [ -f go.sum ] || touch go.sum
COPY go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Generate index during the build (NOT during runtime)
# This command runs cmd/build_index, which expects arguments: <references.json.gz_path> <output_dir>
# /index is the output directory inside the builder stage
RUN go run ./cmd/build_index/ resources/references.json.gz /index

# Compile binaries for server and load balancer
# CGO_ENABLED=0 ensures static compilation
# GOOS=linux GOARCH=amd64 ensures compatibility with the target environment
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/lb ./cmd/lb

# Stage 2: Final image (distroless for minimal size and security)
FROM gcr.io/distroless/static-debian12:nonroot

# Copy compiled binaries from the builder stage
COPY --from=builder /out/server /server
COPY --from=builder /out/lb /lb

# Copy the generated index files from the builder stage
COPY --from=builder /index/ /index/

# Copy configuration files
COPY resources/mcc_risk.json /resources/mcc_risk.json
COPY resources/normalization.json /resources/normalization.json

# Entrypoint is defined in docker-compose.yml, not here,
# to allow different commands for lb and server services.
