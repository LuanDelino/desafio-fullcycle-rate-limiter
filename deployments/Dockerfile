# O contexto de build é a raiz do repositório — veja `context: ..` no compose.

# ---- build ----
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# -s -w tira tabela de símbolos e DWARF: o binário não precisa deles para rodar.
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/server ./cmd/server

# ---- runtime ----
FROM alpine:3.21

RUN adduser -D -u 10001 app
USER app

COPY --from=build /bin/server /bin/server

EXPOSE 8080
ENTRYPOINT ["/bin/server"]
