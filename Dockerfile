FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /fast-matcher .

FROM alpine:3.21
COPY --from=builder /fast-matcher /fast-matcher
ENTRYPOINT ["/fast-matcher"]
