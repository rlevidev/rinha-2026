FROM golang:1.26-alpine AS build
WORKDIR /app
ENV GOEXPERIMENT=simd
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Gerar índice durante o build (NÃO durante runtime)
RUN mkdir -p /index && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go run ./cmd/build_index/ resources/references.json.gz /index/index_p0.bin 0 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go run ./cmd/build_index/ resources/references.json.gz /index/index_p1.bin 1 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go run ./cmd/build_index/ resources/references.json.gz /index/index_p2.bin 2 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go run ./cmd/build_index/ resources/references.json.gz /index/index_p3.bin 3 && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/lb ./cmd/lb

FROM gcr.io/distroless/static-debian12:nonroot AS final
COPY --from=build /out/server /server
COPY --from=build /out/lb /lb
COPY --from=build /index/ /index/
COPY resources/mcc_risk.json /resources/mcc_risk.json
COPY resources/normalization.json /resources/normalization.json
