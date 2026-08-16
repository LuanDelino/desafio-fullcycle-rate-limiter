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

### Onde configurar

Tudo vem de variável de ambiente — não há número de limite escrito no código.
São três pontos de configuração, e o que vale depende de como você está rodando:

| Ponto | Arquivo | Quando vale |
|---|---|---|
| Compose | `docker-compose.yaml`, bloco `environment` do serviço `app` | Rodando com `docker compose up` — **é o ponto que vale por padrão** |
| Arquivo `.env` | `.env` na raiz (copie de `.env.example`) | Rodando o binário direto, fora do Docker |
| Ambiente do processo | variável exportada no shell ou no orquestrador | Sempre; tem precedência sobre o `.env` |

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
da aplicação vira o dobro do limite. Por isso a implementação padrão é Redis.
`store.Memory` existe para teste e para rodar sem Redis — não use com mais de uma
instância.

**O TTL é marcado só quando não existe, e em Go puro.** O `Increment` manda `INCR` e
`PTTL` na mesma transação e só chama `PEXPIRE` quando a chave não tem tempo de vida.
As duas alternativas mais curtas são armadilhas: marcar o TTL a cada requisição
renova a janela para sempre, e sob tráfego contínuo o contador nunca reinicia;
`ExpireNX` do go-redis fala em segundos e arredondaria uma janela de 500ms para 1s
em silêncio. Como o `PEXPIRE` fica fora da transação, ele não é atômico com o
`INCR` — mas a condição é "chave sem TTL", que a requisição seguinte reencontra e
repara. Um script Lua fecharia essa fresta em um passo só; o custo é sair de Go e
levar lógica para dentro do Redis, e a fresta aqui se conserta sozinha.

**Falha do Redis responde 500, não 429 nem 200.** Se o store cai, o limiter devolve
erro e o middleware responde 500 com log. Liberar tudo (fail-open) transformaria uma
queda do Redis em janela aberta justamente na hora em que o sistema está pior;
responder 429 mentiria sobre a causa para o cliente. O ponto de não-uso: sob um
Redis instável isso derruba tráfego legítimo — aí o certo é um circuit breaker no
store com política explícita de degradação, não trocar o default silenciosamente.

**Fatal na subida, recover na requisição.** Config inválida ou Redis fora do ar no
boot matam o processo com código 1: não há recuperação possível em runtime, e um
processo que sobe assim entraria no balanceador fingindo saúde. Já um panic durante
uma requisição é recuperado pelo middleware `Recover`, que é o mais externo da
cadeia — cobre inclusive um panic do próprio limiter. O `net/http` já recupera panic
sozinho e mantém o servidor de pé, mas encerra a conexão sem escrever status: o
cliente vê a resposta cortada em vez de um 500, e o stack sai fora do log
estruturado. `http.ErrAbortHandler` continua propagando, porque ali o abandono
silencioso da resposta é intencional.

**Onde este limiter não resolve.** Ele barra abuso; não expande capacidade — não
substitui capacity planning. E limite apertado demais atrapalha operação normal
antes de proteger qualquer coisa: os números de `RATE_LIMIT_IP` e `RATE_LIMIT_TOKENS`
precisam sair de tráfego medido, não de chute.

## Estrutura

```
cmd/server/             servidor HTTP e escolha da estratégia
internal/config/        leitura do ambiente (nenhum outro pacote lê env)
internal/limiter/       regra de negócio e a interface Store
internal/limiter/store/ implementações Redis e memória
internal/middleware/    rate limit (request -> limiter -> 429) e recover
```

Só Go e a biblioteca padrão na regra de negócio; as dependências externas são o
client do Redis e o leitor de `.env`.
