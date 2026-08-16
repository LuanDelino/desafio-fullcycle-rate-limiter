# Rate Limiter

Rate limiter em Go, aplicado como middleware HTTP, com limite por IP ou por token
de acesso e persistência no Redis. O enunciado está em
[docs/desafio.md](docs/desafio.md).

## Como executar

Toda a infraestrutura — Dockerfile, compose e `.env` — vive em
[`deployments/`](deployments), e os comandos estão no `Makefile`:

```bash
make up
```

Sobe o Redis e a aplicação na porta 8080:

```bash
curl localhost:8080/
```

| Alvo | O que faz |
|---|---|
| `make up` | Sobe Redis e aplicação na porta 8080 |
| `make down` | Derruba tudo e apaga os dados do Redis |
| `make logs` | Acompanha o log da aplicação |
| `make test` | Roda a suíte completa no Docker, com Redis de verdade |
| `make test-verbose` | Igual ao `test`, listando caso a caso |
| `make test-unit` | Roda só o que não precisa de Redis, direto na máquina |
| `make load` | Dispara carga contra o servidor no ar e confere as respostas |
| `make check` | Formata, analisa e roda a suíte completa |
| `make help` | Lista os alvos |

Cada alvo é uma linha de `docker compose -f deployments/docker-compose.yml ...`;
quem preferir chamar o compose direto tem o mesmo resultado.

## Como configurar o limiter

Tudo vem de variável de ambiente — não há limite escrito no código. A leitura
acontece em um lugar só, `internal/config/config.go`. Valor inválido derruba a
subida com a mensagem do que está errado, em vez de cair no padrão em silêncio.

| Ponto | Arquivo | Quando vale |
|---|---|---|
| `.env` do deployment | `deployments/.env` | Rodando com `docker compose` — **é o que vale por padrão** |
| Compose | `deployments/docker-compose.yml` | Guarda só topologia (`REDIS_ADDR`) |
| `.env` da raiz | `.env` na raiz (copie de `deployments/.env.example`) | Rodando o binário fora do Docker |
| Ambiente do processo | variável exportada no shell ou no orquestrador | Sempre; tem precedência sobre qualquer `.env` |

### Limite de requisições

| Variável | Padrão | O que faz |
|---|---|---|
| `RATE_LIMIT_IP` | `10` | Máximo de requisições por janela para um mesmo IP |
| `RATE_LIMIT_TOKENS` | vazio | Limites por token, no formato `token:limite,outro:limite` |

O limite é por **janela**, e a janela é o `RATE_LIMIT_WINDOW` abaixo. Com o padrão
de `1s`, `RATE_LIMIT_IP=10` significa 10 req/s.

```
RATE_LIMIT_IP=10
RATE_LIMIT_TOKENS=abc123:100,def456:5
```

Para acrescentar um token, some um par à lista. Token que não estiver na lista cai
no limite do IP.

### Tempo de vida no Redis (TTL)

Os dois tempos abaixo são o TTL das chaves gravadas no Redis: mudar a variável
muda o tempo de vida da chave correspondente.

| Variável | Padrão | Chave no Redis | O que o TTL significa |
|---|---|---|---|
| `RATE_LIMIT_WINDOW` | `1s` | `ratelimit:count:<identidade>` | Enquanto a chave viver, as requisições somam no mesmo contador; quando vence, a contagem recomeça |
| `RATE_LIMIT_BLOCK_DURATION` | `5m` | `ratelimit:block:<identidade>` | Enquanto a chave viver, toda requisição da identidade é rejeitada com 429 |

`<identidade>` é `ip:<endereço>` ou `token:<token>`. Os dois campos aceitam
qualquer duração no formato do Go: `500ms`, `1s`, `30s`, `5m`, `1h`.

O bloqueio precisa durar mais que a janela para ter efeito: mais curto, ele
expiraria antes dela. Para conferir os TTLs com o sistema no ar:

```bash
docker exec rate-limiter-redis redis-cli KEYS 'ratelimit:*'
docker exec rate-limiter-redis redis-cli PTTL ratelimit:count:ip:172.20.0.1
```

### Servidor e conexão

| Variável | Padrão | O que faz |
|---|---|---|
| `WEB_SERVER_PORT` | `8080` | Porta do servidor HTTP |
| `RATE_LIMIT_STORE` | `redis` | Estratégia de persistência: `redis` ou `memory` |
| `REDIS_ADDR` | `localhost:6379` | Endereço do Redis (`redis:6379` dentro do compose) |
| `REDIS_PASSWORD` | vazio | Senha do Redis |
| `REDIS_DB` | `0` | Banco do Redis |

## Como funciona

O middleware é o filtro mais externo: decide antes de qualquer autenticação ou
trabalho de handler. De cada requisição ele tira o IP de origem (sem a porta) e o
header `API_KEY`, e chama o limiter, que escolhe uma identidade e um teto:

1. Token presente **e cadastrado** em `RATE_LIMIT_TOKENS` — vale o limite do token.
2. Qualquer outro caso — vale o limite do IP.

Contado o acesso, se o total da janela passou do teto, a identidade é marcada como
bloqueada por `RATE_LIMIT_BLOCK_DURATION` e a resposta é `429` com o corpo:

```
you have reached the maximum number of requests or actions allowed within a certain time frame
```

Contador e bloqueio são chaves separadas, com TTLs próprios: virar a janela não
solta quem foi bloqueado.

## Como alterar a estratégia de persistência

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
3. Aponte `RATE_LIMIT_STORE` para o nome novo.

Nada no limiter e nada no middleware muda. As duas implementações que já existem
(`store.Redis` e `store.Memory`) são a prova de que a costura é essa e só essa, e
a `ContratoSuite` roda as mesmas asserções contra as duas.

## Como testar

```bash
make test
```

Os testes usam [testify](https://github.com/stretchr/testify), em uma suíte por
unidade:

| Suíte | O que cobre |
|---|---|
| `AceitacaoSuite` | O desafio ponta a ponta: servidor HTTP e Redis de verdade, pela mesma montagem de middlewares do `main` |
| `ConfigSuite` | Padrões, leitura de cada variável e recusa de valor inválido |
| `LimiterSuite` | Precedência token/IP, contagem, virada de janela e bloqueio, com relógio controlado |
| `ContratoSuite` | As mesmas asserções contra Redis e memória |
| `RedisSuite` | Tempo de vida real das chaves e reparo de chave sem expiração |
| `RateLimitSuite` | Extração de IP e token, 429 com a mensagem exigida, 500 na falha do limiter |
| `RecoverSuite` | Panic vira 500 sem derrubar o servidor |

As suítes que precisam de Redis são puladas quando `REDIS_TEST_ADDR` não está
definido, então `go test ./...` fora do Docker roda só o que não depende dele.

### Carga contra o servidor no ar

Com o sistema de pé, o `loadcheck` dispara requisições concorrentes de fora do
processo e **confere** o resultado:

```bash
make load
```

```
--- limite por IP: 10/s ---
→ 50 requisições, 25 simultâneas, sem token (limite por IP)
  200 aceitas: 10
  429 recusadas: 40
  latência: mediana 3.7ms, p95 12.5ms, máxima 12.6ms
  duração: 13ms (3812 req/s)
  OK: 10 aceitas e 40 recusadas, como esperado
```

Ele compara as aceitas com o esperado, confere o corpo de cada 429 contra o texto
do desafio e sai com código diferente de zero quando diverge. Para apontar em
outro alvo ou outro limite:

```bash
go run ./cmd/loadcheck -n 150 -c 50 -token abc123 -esperado 100
```

| Flag | Padrão | O que faz |
|---|---|---|
| `-url` | `http://localhost:8080/` | Endereço a chamar |
| `-n` | `50` | Quantas requisições disparar no total |
| `-c` | `10` | Quantas correm ao mesmo tempo |
| `-token` | vazio | Valor do header `API_KEY`; vazio testa o limite por IP |
| `-esperado` | `10` | Quantas requisições **devem** ser aceitas |

Limpe o Redis entre execuções — quem estourou o limite fica bloqueado por
`RATE_LIMIT_BLOCK_DURATION`, e a execução seguinte encontraria zero aceitas. O
`make load` já faz isso entre os cenários.

## Decisões

| Decisão | Por quê |
|---|---|
| Janela fixa, não token bucket | O desafio pede rejeitar o excesso; token bucket enfileira. Custo assumido: na virada da janela cabe até 2x o limite num intervalo curto |
| Token não cadastrado cai no limite do IP | Se qualquer token inventado ganhasse limite próprio, bastaria mandar um header para furar o limite por IP |
| Contador no Redis, não no processo | Com duas instâncias da aplicação, contador em memória viraria o dobro do limite |
| TTL marcado só quando a chave não tem | Marcar a cada requisição renovaria a janela para sempre, e o contador nunca reiniciaria |
| Contador e bloqueio em chaves separadas | TTLs independentes: a janela vira, a punição continua de pé |
| Falha do Redis responde 500 | Liberar tudo abriria a porteira justo quando o sistema está pior; 429 mentiria sobre a causa |
| Fatal na subida, recover na requisição | Sem config ou store não há recuperação possível; já um panic de requisição não pode derrubar o servidor |
| Interfaces com até 3 métodos | Acima disso a interface vira o desenho de uma implementação, e só o Redis a satisfaria |
| Estratégia escolhida uma vez, sem troca a quente | A estratégia guarda o estado; trocá-la em runtime descartaria os bloqueios ativos |

Onde este limiter **não** resolve: ele barra abuso, não expande capacidade — os
números precisam sair de tráfego medido. E não lê `X-Forwarded-For`, então atrás
de um proxy todos os clientes contariam como um IP só.

## Estrutura

```
Makefile                atalhos para o compose
cmd/loadcheck/          carga com veredito contra o servidor no ar
cmd/server/             servidor HTTP e escolha da estratégia
deployments/            Dockerfile, compose e .env
internal/config/        leitura do ambiente (nenhum outro pacote lê env)
internal/limiter/       regra de negócio e a interface Store
internal/limiter/store/ implementações Redis e memória
internal/middleware/    rate limit (request -> limiter -> 429) e recover
```
