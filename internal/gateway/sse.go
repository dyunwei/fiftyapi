package gateway

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

func consumeSSE(reader io.Reader, consume func(string, []byte) error) error {
	limited := &io.LimitedReader{R: reader, N: 32 << 20}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 8192), 2<<20)
	event := ""
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			event = ""
			return nil
		}
		payload := []byte(strings.TrimSuffix(data.String(), "\n"))
		e := event
		data.Reset()
		event = ""
		return consume(e, payload)
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			data.WriteByte('\n')
		}
	}
	if scanner.Err() != nil {
		return scanner.Err()
	}
	if limited.N <= 0 {
		return errors.New("流式响应超过大小限制")
	}
	return flush()
}
func emitSSE(w http.ResponseWriter, event string, data []byte) error {
	controller := http.NewResponseController(w)
	controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if event != "" {
		if _, err := io.WriteString(w, "event: "+event+"\n"); err != nil {
			return err
		}
	}
	if _, err := w.Write(append(append([]byte("data: "), data...), []byte("\n\n")...)); err != nil {
		return err
	}
	return flushResponse(w)
}

func flushResponse(w http.ResponseWriter) error {
	err := http.NewResponseController(w).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}
