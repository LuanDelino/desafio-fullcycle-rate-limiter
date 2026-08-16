# Rate Limiter

Rate limiter em Go, aplicado como middleware HTTP, com limite por IP ou por token
de acesso e persistência no Redis. O enunciado do desafio está em
[docs/desafio.md](docs/desafio.md).

## Como rodar

Tudo que é infraestrutura — Dockerfile, compose e `.env` — vive em
[`deployments/`](deployments), e os comandos do dia a dia estão no `Makefile`:

```bash
make up
```

Sobe o Redis e a aplicação na porta 8080. Para conferir:

```bash
curl localhost:8080/
```

| Alvo | O que faz |
|---|---|
| `make up` | Sobe Redis e aplicação na porta 8080 |
| `make down` | Derruba tudo e apaga os dados do Redis |
| `make logs` | Acompanha o log da aplicação |
| `make test` | Roda a suíte completa no Docker, com Redis de verdade |
| `make test-unit` | Roda só o que não precisa de Redis, direto na máquina |
| `make check` | Formata, analisa e roda a suíte completa |
| `make help` | Lista os alvos |

O Makefile é conveniência sobre o compose, não uma segunda forma de configurar:
cada alvo é uma linha de `docker compose -f deployments/docker-compose.yml ...`.
Quem preferir chamar o compose direto tem o mesmo resultado.

## Como testar

Toda a suíte, incluindo os testes de integração contra um Redis de verdade:

```bash
make test
```

Os testes usam [testify](https://github.com/stretchr/testify), organizados em uma
suíte por unidade: `ConfigSuite`, `LimiterSuite`, `RedisSuite`, `RateLimitSuite` e
`RecoverSuite`. A `RedisSuite` é pulada inteira quando `REDIS_TEST_ADDR` não está
definido, então `go test ./...` fora do Docker roda só a parte que não precisa de
Redis.

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

### Onde configurar

Tudo vem de variável de ambiente — não há número de limite escrito no código.
São três pontos de configuração, e o que vale depende de como você está rodando:

| Ponto | Arquivo | Quando vale |
|---|---|---|
| `.env` do deployment | `deployments/.env` | Rodando com `docker compose` — **é o ponto que vale por padrão** |
| Compose | `deployments/docker-compose.yml`, bloco `environment` do serviço `app` | Sempre que se roda pelo compose; guarda só topologia (`REDIS_ADDR`) |
| `.env` da raiz | `.env` na raiz (copie de `deployments/.env.example`) | Rodando o binário direto, fora do Docker |
| Ambiente do processo | variável exportada no shell ou no orquestrador | Sempre; tem precedência sobre qualquer `.env` |

A divisão entre as duas primeiras linhas é proposital: **política** (limites e
tempos) fica no `.env`, que é onde se mexe; **topologia** (o endereço do Redis)
fica no compose, porque é o nome do serviço ao lado e não é para ser
reconfigurado por fora. O `.env` é opcional — sem ele a aplicação sobe nos
padrões declarados em `internal/config`.

A leitura acontece em um lugar só, `internal/config/config.go`. Nenhum outro
pacote consulta o ambiente, então esse arquivo é a lista completa do que o
sistema lê. Variável inválida derruba a subida com a mensagem do que está errado,
em vez de silenciosamente cair no padrão.

### Limite máximo de requisições

| Variável | Padrão | O que faz |
|---|---|---|
| `RATE_LIMIT_IP` | `10` | Máximo de requisições por janela para um mesmo IP |
| `RATE_LIMIT_TOKENS` | vazio | Limites por token, no formato `token:limite,outro:limite` |

O limite é sempre **requisições por janela**, e a janela é o `RATE_LIMIT_WINDOW`
abaixo. Com o padrão de `1s`, `RATE_LIMIT_IP=10` significa 10 req/s.

```
RATE_LIMIT_IP=10
RATE_LIMIT_TOKENS=abc123:100,def456:5
```

Para acrescentar um token, some um par à lista. Token que não estiver na lista
não tem limite próprio: cai no limite do IP. Limite tem que ser inteiro positivo;
par malformado derruba a subida.

### Tempo de vida no Redis (TTL)

Os dois tempos abaixo são exatamente o TTL das chaves gravadas no Redis. Não há
TTL escondido nem valor fixo no código — mudar a variável muda o tempo de vida da
chave correspondente.

| Variável | Padrão | Chave no Redis | O que o TTL significa |
|---|---|---|---|
| `RATE_LIMIT_WINDOW` | `1s` | `ratelimit:count:<identidade>` | Enquanto a chave viver, as requisições somam no mesmo contador. Quando ela vence, a contagem recomeça do zero |
| `RATE_LIMIT_BLOCK_DURATION` | `5m` | `ratelimit:block:<identidade>` | Enquanto a chave viver, toda requisição da identidade é rejeitada com 429. Quando ela vence, a identidade volta a ser atendida |

`<identidade>` é `ip:<endereço>` ou `token:<token>`, conforme a regra de
precedência. Os dois campos aceitam qualquer duração no formato do Go: `500ms`,
`1s`, `30s`, `5m`, `1h`.

Os TTLs são independentes de propósito. O contador é curto porque mede vazão; o
bloqueio é longo porque é punição. É isso que faz o bloqueio sobreviver à virada
da janela: o contador de 1s expira, a punição de 5min continua de pé.

Para conferir o TTL real das chaves com o sistema no ar:

```bash
docker exec rate-limiter-redis redis-cli KEYS 'ratelimit:*'
docker exec rate-limiter-redis redis-cli PTTL ratelimit:count:ip:172.20.0.1
docker exec rate-limiter-redis redis-cli PTTL ratelimit:block:ip:172.20.0.1
docker exec rate-limiter-redis redis-cli PTTL ratelimit:count:token:abc123
```

`PTTL` responde em milissegundos; `-1` seria chave sem expiração e `-2`, chave
inexistente.

### Servidor e conexão

| Variável | Padrão | O que faz |
|---|---|---|
| `WEB_SERVER_PORT` | `8080` | Porta do servidor HTTP |
| `RATE_LIMIT_STORE` | `redis` | Estratégia de persistência: `redis` ou `memory` |
| `REDIS_ADDR` | `localhost:6379` | Endereço do Redis (`redis:6379` dentro do compose) |
| `REDIS_PASSWORD` | vazio | Senha do Redis |
| `REDIS_DB` | `0` | Banco do Redis |

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

### A régua das interfaces

O projeto tem duas interfaces, e as duas cabem em no máximo três métodos:

| Interface | Onde | Métodos |
|---|---|---|
| `limiter.Store` | `internal/limiter` | `Increment`, `Block`, `Blocked` |
| `middleware.Checker` | `internal/middleware` | `Allow` |

Três é o teto. Interface que cresce além disso deixa de ser um ponto de troca e
vira o desenho de uma implementação específica — na prática, só o Redis
conseguiria satisfazê-la, e a Strategy viraria enfeite. Se um dia faltar um
método aqui, a saída é uma segunda interface pequena para quem precisa dele, não
um quarto método nesta.

Ambas são declaradas no **consumidor**, não junto de quem as implementa: é o
`limiter` que diz do que precisa de um store, e o `middleware` que diz do que
precisa do limiter.

### Duas diferenças em relação ao Strategy canônico

O [exemplo de Strategy em Go do refactoring.guru](https://refactoring.guru/design-patterns/strategy/go/example)
declara a interface junto das implementações e expõe um `setEvictionAlgo` para
trocar a estratégia em runtime. Aqui os dois pontos são diferentes, de propósito:

**A interface fica no consumidor.** Em Go, quem depende declara o que precisa —
o produtor não impõe a abstração. É o que permite o `middleware` depender de
`Checker`, com um método, sem arrastar os três do `Store` junto.

**Não há troca em runtime.** No exemplo, a estratégia é o algoritmo de despejo:
não guarda estado, então trocar no meio da execução é inofensivo. Aqui a
estratégia **é o estado** — contadores e bloqueios vivem dentro dela. Um setter
que trocasse Redis por memória em produção descartaria todo bloqueio ativo e
soltaria na hora quem estava punido. A escolha acontece uma vez, na composição
(`buildStore`), e não se mexe depois.

Essa é a régua para julgar o padrão: Strategy pede estratégias intercambiáveis,
não necessariamente trocáveis a quente. O que garante a primeira parte é a
`ContratoSuite`, que roda as mesmas asserções contra Redis e memória.

## Estrutura

```
Makefile                atalhos para o compose
cmd/server/             servidor HTTP e escolha da estratégia
deployments/            Dockerfile, compose e .env
internal/config/        leitura do ambiente (nenhum outro pacote lê env)
internal/limiter/       regra de negócio e a interface Store
internal/limiter/store/ implementações Redis e memória
internal/middleware/    rate limit (request -> limiter -> 429) e recover
```

Infraestrutura concentrada em `deployments/` para que a raiz mostre o que o
projeto **é**, e não como ele é empacotado. O `deployments/.env` é versionado de
propósito: são políticas de limite, não segredos, e o desafio pede que o projeto
suba só com Docker. No dia em que entrar credencial de verdade ali, o arquivo sai
do git e o valor passa a vir do orquestrador.

## Testes

| Suíte | Onde | O que cobre |
|---|---|---|
| `AceitacaoSuite` | `cmd/server` | O desafio ponta a ponta: servidor HTTP e Redis de verdade, com a mesma montagem de middlewares que o `main` usa |
| `ConfigSuite` | `internal/config` | Padrões, leitura de cada variável, parse dos limites por token e recusa de valor inválido |
| `LimiterSuite` | `internal/limiter` | Precedência token/IP, contagem, virada de janela, bloqueio e falha do store — com relógio controlado, sem espera real |
| `ContratoSuite` | `internal/limiter/store` | As mesmas asserções contra Redis e memória: é o que sustenta a premissa da Strategy |
| `RedisSuite` | `internal/limiter/store` | Só o que o contrato não alcança: tempo de vida real das chaves, janela sub-segundo e reparo de chave sem expiração |
| `RateLimitSuite` | `internal/middleware` | Extração de IP e token, 200 no caminho feliz, 429 com a mensagem exigida, 500 na falha do limiter |
| `RecoverSuite` | `internal/middleware` | Panic vira 500, requisição normal atravessa intacta, panic do limiter é coberto, `ErrAbortHandler` segue propagando |

Cada suíte prova o que só ela pode provar, e nada além disso. A regra é provada em
memória, com relógio injetado e sem I/O; o comportamento comum às estratégias é
provado uma vez, para as duas, no contrato; o tempo de vida real das chaves é
provado contra o Redis; e o que o `docs/desafio.md` exige é provado por HTTP.

A `AceitacaoSuite` é a que responde "funciona?" — vale destacar três casos dela:

- **Contagem exata**: dispara `limite + 5` requisições e exige que passem
  exatamente `limite`. Um rate limiter que erra por um só aparece assim.
- **Concorrência**: 60 requisições disparadas ao mesmo tempo contra um limite de
  25 têm de resultar em 25 aceitas. É o cenário de produção, e é onde uma
  contagem não atômica deixaria passar mais que o limite.
- **O 429 literal**: status e corpo conferidos contra o texto exigido no desafio,
  palavra por palavra.

Elas rodam limpas sob `-race`, e a suíte é sensível: trocar `count > limit` por
`count > limit+1` no limiter quebra `LimiterSuite` e `AceitacaoSuite` — duas
camadas independentes pegam a mesma regressão.

