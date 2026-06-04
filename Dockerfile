FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Build index and binaries in a dedicated /build directory
RUN mkdir -p /build/index
RUN go run ./cmd/build_index/ resources/references.json.gz /build/index
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /build/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /build/lb ./cmd/lb

FROM gcr.io/distroless/static-debian12:nonroot
# Copy from /build, ensure destination paths match expected app paths
COPY --from=builder /build/server /server
COPY --from=builder /build/lb /lb
COPY --from=builder /build/index /index
COPY resources/mcc_risk.json /resources/mcc_risk.json
COPY resources/normalization.json /resources/normalization.json
