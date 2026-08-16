// Command loadcheck dispara carga concorrente contra um rate limiter no ar e
// confere se ele respondeu como devia.
//
// A diferença para um gerador de carga comum é o veredito: aqui o número de
// requisições aceitas é comparado com o esperado, o corpo do 429 é conferido
// contra o texto exigido pelo desafio, e o processo sai com código 1 quando algo
// diverge. Serve para rodar contra o servidor de verdade, fora dos testes.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

const mensagemEsperada = "you have reached the maximum number of requests or actions allowed within a certain time frame"

type resultado struct {
	status  int
	corpo   string
	latenza time.Duration
}

func main() {
	url := flag.String("url", "http://localhost:8080/", "endereço a chamar")
	total := flag.Int("n", 50, "número de requisições")
	simultaneas := flag.Int("c", 10, "quantas correm ao mesmo tempo")
	token := flag.String("token", "", "valor do header API_KEY; vazio testa o limite por IP")
	esperado := flag.Int("esperado", 10, "quantas requisições devem ser aceitas")
	flag.Parse()

	if *total < *esperado {
		fmt.Fprintf(os.Stderr, "erro: -n (%d) precisa ser maior que -esperado (%d) para provar o corte\n", *total, *esperado)
		os.Exit(2)
	}

	fmt.Printf("→ %d requisições, %d simultâneas, %s\n", *total, *simultaneas, identidade(*token))

	resultados, duracao := disparar(*url, *token, *total, *simultaneas)
	relatar(resultados, duracao)

	if problemas := conferir(resultados, *esperado, *total); len(problemas) > 0 {
		for _, problema := range problemas {
			fmt.Printf("  FALHA: %s\n", problema)
		}
		os.Exit(1)
	}

	fmt.Printf("  OK: %d aceitas e %d recusadas, como esperado\n", *esperado, *total-*esperado)
}

func identidade(token string) string {
	if token == "" {
		return "sem token (limite por IP)"
	}
	return "token " + token
}

func disparar(url, token string, total, simultaneas int) ([]resultado, time.Duration) {
	var (
		grupo       sync.WaitGroup
		mu          sync.Mutex
		resultados  = make([]resultado, 0, total)
		vagas       = make(chan struct{}, simultaneas)
		client      = &http.Client{Timeout: 10 * time.Second}
		largada     = make(chan struct{})
		comecou     time.Time
		ctx, cancel = context.WithTimeout(context.Background(), time.Minute)
	)
	defer cancel()

	for i := 0; i < total; i++ {
		grupo.Add(1)
		go func() {
			defer grupo.Done()

			<-largada
			vagas <- struct{}{}
			defer func() { <-vagas }()

			r := chamar(ctx, client, url, token)

			mu.Lock()
			resultados = append(resultados, r)
			mu.Unlock()
		}()
	}

	comecou = time.Now()
	close(largada)
	grupo.Wait()

	return resultados, time.Since(comecou)
}

func chamar(ctx context.Context, client *http.Client, url, token string) resultado {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return resultado{status: -1, corpo: err.Error()}
	}
	if token != "" {
		req.Header.Set("API_KEY", token)
	}

	inicio := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return resultado{status: -1, corpo: err.Error(), latenza: time.Since(inicio)}
	}
	defer func() { _ = resp.Body.Close() }()

	corpo, _ := io.ReadAll(resp.Body)

	return resultado{status: resp.StatusCode, corpo: string(corpo), latenza: time.Since(inicio)}
}

func relatar(resultados []resultado, duracao time.Duration) {
	porStatus := make(map[int]int)
	latencias := make([]time.Duration, 0, len(resultados))
	for _, r := range resultados {
		porStatus[r.status]++
		latencias = append(latencias, r.latenza)
	}

	statuses := make([]int, 0, len(porStatus))
	for status := range porStatus {
		statuses = append(statuses, status)
	}
	sort.Ints(statuses)

	for _, status := range statuses {
		fmt.Printf("  %s: %d\n", nomeDoStatus(status), porStatus[status])
	}

	sort.Slice(latencias, func(i, j int) bool { return latencias[i] < latencias[j] })
	if len(latencias) > 0 {
		fmt.Printf("  latência: mediana %v, p95 %v, máxima %v\n",
			latencias[len(latencias)/2],
			latencias[percentil(len(latencias), 95)],
			latencias[len(latencias)-1],
		)
	}
	fmt.Printf("  duração: %v (%.0f req/s)\n", duracao.Round(time.Millisecond), float64(len(resultados))/duracao.Seconds())
}

func percentil(tamanho, p int) int {
	indice := tamanho * p / 100
	if indice >= tamanho {
		indice = tamanho - 1
	}
	return indice
}

func nomeDoStatus(status int) string {
	switch status {
	case -1:
		return "erro de conexão"
	case http.StatusOK:
		return "200 aceitas"
	case http.StatusTooManyRequests:
		return "429 recusadas"
	default:
		return fmt.Sprintf("%d inesperado", status)
	}
}

// conferir é o oráculo: sem ele o programa seria só um gerador de carga.
func conferir(resultados []resultado, esperado, total int) []string {
	var (
		problemas []string
		aceitas   int
		recusadas int
	)

	for _, r := range resultados {
		switch r.status {
		case http.StatusOK:
			aceitas++
		case http.StatusTooManyRequests:
			recusadas++
			if r.corpo != mensagemEsperada {
				problemas = append(problemas, fmt.Sprintf("corpo do 429 diferente do exigido: %q", r.corpo))
			}
		case -1:
			problemas = append(problemas, "requisição não completou: "+r.corpo)
		default:
			problemas = append(problemas, fmt.Sprintf("status inesperado %d", r.status))
		}
	}

	if aceitas != esperado {
		problemas = append(problemas, fmt.Sprintf("%d requisições aceitas, esperado exatamente %d", aceitas, esperado))
	}
	if recusadas != total-esperado {
		problemas = append(problemas, fmt.Sprintf("%d requisições recusadas, esperado %d", recusadas, total-esperado))
	}

	return dedup(problemas)
}

func dedup(problemas []string) []string {
	vistos := make(map[string]bool, len(problemas))
	unicos := problemas[:0]

	for _, p := range problemas {
		if !vistos[p] {
			vistos[p] = true
			unicos = append(unicos, p)
		}
	}
	return unicos
}
