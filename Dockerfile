FROM golang:1.26-alpine AS builder
WORKDIR /app

COPY go.mod ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /bin/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /bin/lb ./cmd/lb

FROM scratch
COPY --from=builder /bin/api /api
COPY --from=builder /bin/lb /lb
COPY resources/normalization.json /resources/normalization.json
COPY resources/mcc_risk.json /resources/mcc_risk.json
COPY index/ /index/

EXPOSE 9999
