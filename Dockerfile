FROM golang:1.17-alpine AS builder

RUN     mkdir /app
WORKDIR /app
# ADD     go.mod main.go /app/
COPY    go.mod ./
COPY    go.sum ./
RUN     go mod download

COPY    *.go ./

RUN     go build -o ./udp-echo

FROM golang:1.17-alpine AS runner

RUN mkdir /app/ 

WORKDIR /app
COPY --from=builder /app/udp-echo ./

CMD ["udp-echo"]
