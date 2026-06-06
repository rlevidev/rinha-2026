FROM golang:1.26-alpine AS build
WORKDIR /app
ENV GOEXPERIMENT=simd \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    GOAMD64=v3
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Gerar índice durante o build concorrentemente
RUN mkdir -p /index && \
    go build -trimpath -ldflags="-s -w" -o /out/build_index ./cmd/build_index && \
    for i in $(seq 0 11); do \
      /out/build_index resources/references.json.gz /index/index_p$i.bin $i & \
    done; \
    wait && \
    go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
    go build -trimpath -ldflags="-s -w" -o /out/lb ./cmd/lb

FROM gcr.io/distroless/static-debian12:nonroot AS final
COPY --from=build /out/server /server
COPY --from=build /out/lb /lb
COPY --from=build /index/ /index/
COPY resources/mcc_risk.json /resources/mcc_risk.json
COPY resources/normalization.json /resources/normalization.json
