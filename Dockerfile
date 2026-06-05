FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Gerar índice durante o build (NÃO durante runtime)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go run ./cmd/build_index/ resources/references.json.gz /index && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/lb ./cmd/lb

FROM gcr.io/distroless/static-debian12:nonroot AS server_final
COPY --from=build /out/server /server
COPY --from=build /index/ /index/
COPY resources/mcc_risk.json /resources/mcc_risk.json
COPY resources/normalization.json /resources/normalization.json

FROM gcr.io/distroless/static-debian12:nonroot AS lb_final
COPY --from=build /out/lb /lb
