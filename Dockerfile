# build
FROM golang:1.18-alpine AS builder

RUN     mkdir /app
WORKDIR /app

COPY    go.mod ./
COPY    go.sum ./
RUN     go mod download

COPY    . ./

RUN     go build -o ./gw

# deploy
FROM alpine AS runner

RUN mkdir /app/

WORKDIR /app
COPY --from=builder /app/gw ./

CMD ["./gw"]
