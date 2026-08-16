# Rate Limiter

Rate limiter em Go, aplicado como middleware HTTP, com limite por IP ou por token
de acesso e persistência no Redis. O enunciado do desafio está em
[docs/desafio.md](docs/desafio.md).

## Como rodar

```bash
docker compose up -d --build
```

Sobe o Redis e a aplicação na porta 8080. Para conferir:

```bash
curl localhost:8080/
```

## Como testar

Toda a suíte, incluindo os testes de integração contra um Redis de verdade:

```bash
docker compose run --rm test
```

Os testes de integração são pulados quando `REDIS_TEST_ADDR` não está definido,
então `go test ./...` fora do Docker roda só a parte que não precisa de Redis.

Verificação manual do limite por IP (padrão de 10 req/s):

```bash
for i in $(seq 1 12); do curl -s -o /dev/null -w "%{http_code}\n" localhost:8080/; done
```

As 10 primeiras devolvem 200 e as seguintes 429. Com o token `abc123`, que vale
100 req/s, as mesmas 12 requisições passam:

```bash
for i in $(seq 1 12); do
  curl -s -o /dev/null -w "%{http_code}\n" -H "API_KEY: abc123" localhost:8080/
done
```

## Configuração

Tudo vem de variável de ambiente. Copie `.env.example` para `.env` na raiz para
rodar fora do compose; dentro do compose os valores estão em `docker-compose.yaml`.

| Variável | Padrão | O que faz |
|---|---|---|
| `WEB_SERVER_PORT` | `8080` | Porta do servidor HTTP |
| `RATE_LIMIT_STORE` | `redis` | Estratégia de persistência: `redis` ou `memory` |
| `REDIS_ADDR` | `localhost:6379` | Endereço do Redis |
| `REDIS_PASSWORD` | vazio | Senha do Redis |
| `REDIS_DB` | `0` | Banco do Redis |
| `RATE_LIMIT_IP` | `10` | Máximo de requisições por janela para um mesmo IP |
| `RATE_LIMIT_TOKENS` | vazio | Limites por token: `token:limite,outro:limite` |
| `RATE_LIMIT_WINDOW` | `1s` | Tamanho da janela de contagem |
| `RATE_LIMIT_BLOCK_DURATION` | `5m` | Tempo de bloqueio de quem estourou o limite |

Exemplo de limites por token:

```
RATE_LIMIT_TOKENS=abc123:100,def456:5
```

## Como funciona

O middleware é o filtro mais externo da requisição: ele decide antes de qualquer
autenticação ou trabalho de handler. De cada requisição ele tira o IP de origem
(sem a porta) e o header `API_KEY`, e chama o limiter.

O limiter escolhe uma identidade e um teto:

1. Token presente **e cadastrado** em `RATE_LIMIT_TOKENS` — vale o limite do token.
2. Qualquer outro caso — vale o limite do IP.

Contado o acesso, se o total da janela passou do teto, a identidade é marcada como
bloqueada por `RATE_LIMIT_BLOCK_DURATION` e a resposta é `429` com o corpo:

```
you have reached the maximum number of requests or actions allowed within a certain time frame
```

O bloqueio é uma marca separada do contador, com TTL próprio. É por isso que virar
a janela não solta quem foi bloqueado: o contador de 1s expira, a punição de 5min
continua de pé.

## Como trocar a estratégia de persistência

A lógica de negócio conhece só esta interface, declarada em
`internal/limiter/limiter.go`:

```go
type Store interface {
    Increment(ctx context.Context, key string, window time.Duration) (int64, error)
    Block(ctx context.Context, key string, duration time.Duration) error
    Blocked(ctx context.Context, key string) (bool, error)
}
```

Para trocar o Redis por Memcached, Postgres ou o que for:

1. Crie o tipo em `internal/limiter/store/` implementando os três métodos.
2. Adicione o caso em `buildStore`, em `cmd/server/main.go`.

Nada no limiter e nada no middleware muda. As duas implementações que já existem
(`store.Redis` e `store.Memory`) são a prova de que a costura é essa e só essa.

## Decisões

**Janela fixa, não token bucket.** O desafio define o limite como "requisições por
segundo" e manda rejeitar o excesso. Janela fixa é `INCR` mais TTL — duas operações
e nenhum estado a manter. Token bucket (`golang.org/x/time/rate`) daria controle de
rajada, mas a API dele conveniente é a que *enfileira* a requisição, e enfileirar é
o oposto do que o desafio pede. O custo assumido: na virada da janela é possível
concentrar até 2x o limite em um intervalo curto. Para o objetivo de barrar abuso,
isso não muda nada; para garantia de vazão constante em um backend frágil, mudaria,
e aí a escolha certa seria token bucket ou sliding window.

**Token não cadastrado cai no limite do IP.** Se um token desconhecido ganhasse um
limite padrão próprio, bastaria mandar um header inventado — e um diferente a cada
requisição — para o limite por IP virar decoração. O limiter roda antes da
autenticação e não tem como saber se o token é legítimo; só pode confiar no que
está cadastrado.

**O contador é do Redis, não do processo.** Contador em memória com duas instâncias
da aplicação vira o dobro do limite. Por isso a implementação padrão é Redis, e o
`Increment` é um script Lua: `INCR` e `PEXPIRE` precisam ser um passo atômico, senão
um processo que morre entre os dois deixa a chave sem expiração e prende a
identidade na primeira janela para sempre. `store.Memory` existe para teste e para
rodar sem Redis — não use com mais de uma instância.

**Falha do Redis responde 500, não 429 nem 200.** Se o store cai, o limiter devolve
erro e o middleware responde 500 com log. Liberar tudo (fail-open) transformaria uma
queda do Redis em janela aberta justamente na hora em que o sistema está pior;
responder 429 mentiria sobre a causa para o cliente. O ponto de não-uso: sob um
Redis instável isso derruba tráfego legítimo — aí o certo é um circuit breaker no
store com política explícita de degradação, não trocar o default silenciosamente.

**Onde este limiter não resolve.** Ele barra abuso; não expande capacidade — não
substitui capacity planning. E limite apertado demais atrapalha operação normal
antes de proteger qualquer coisa: os números de `RATE_LIMIT_IP` e `RATE_LIMIT_TOKENS`
precisam sair de tráfego medido, não de chute.

## Estrutura

```
cmd/server/            servidor HTTP e escolha da estratégia
internal/config/       leitura do ambiente (nenhum outro pacote lê env)
internal/limiter/      regra de negócio e a interface Store
internal/limiter/store/ implementações Redis e memória
internal/middleware/   tradução HTTP: request -> limiter -> 429
```
