FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Gerar índice durante o build
RUN go run ./cmd/build_index/ resources/references.json.gz /index
# Compilar binários
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/lb ./cmd/lb

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/server /server
COPY --from=builder /out/lb /lb
COPY --from=builder /index/ /index/
COPY resources/mcc_risk.json /resources/mcc_risk.json
COPY resources/normalization.json /resources/normalization.json
