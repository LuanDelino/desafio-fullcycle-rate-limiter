package middleware_test

import (
	"log/slog"
	"os"
	"testing"
)

// TestMain silencia o log do pacote durante os testes. Metade das suítes daqui
// exercita justamente os caminhos que registram erro e stack, e esse log é
// comportamento correto — mas na saída do teste é ruído que esconde a falha de
// verdade.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))

	os.Exit(m.Run())
}
