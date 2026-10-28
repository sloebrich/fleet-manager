FROM golang:1.26.4 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o controller ./cmd/controller 
RUN go build -o vehicle ./cmd/vehicle

FROM debian:bookworm-slim AS runtime

WORKDIR /app

COPY --from=builder /app/controller .
COPY --from=builder /app/vehicle .
COPY --from=builder /app/scenarios ./scenarios

CMD ["/app/controller"]
