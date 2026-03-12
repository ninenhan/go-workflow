package units

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// ReadableUnit outputs an io.Reader for chunked consumption.
type ReadableUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*ReadableUnit)(nil)

func (t *ReadableUnit) GetUnitName() string {
	return reflect.TypeOf(ReadableUnit{}).Name()
}

func (t *ReadableUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, errors.New("ReadableUnit: missing input")
	}
	reader, err := toReader(ctx, self.Input.Data)
	if err != nil {
		return nil, err
	}
	return &unit.ExecutionResult{
		NodeName: t.UnitName,
		Data:     reader,
		Stream:   true,
	}, nil
}

func (t *ReadableUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewReadableUnit() ReadableUnit {
	unit := ReadableUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}

func init() {
	unit.RegisterUnitFactory("ReadableUnit", func() unit.ExecutableUnit {
		unit := &ReadableUnit{}
		unit.UnitName = unit.GetUnitName()
		return unit
	})
}

func toReader(ctx context.Context, data any) (io.Reader, error) {
	switch v := data.(type) {
	case io.Reader:
		return v, nil
	case []byte:
		return bytes.NewReader(v), nil
	case string:
		return strings.NewReader(v), nil
	case []string:
		return streamFromSlices(ctx, v), nil
	case []any:
		strs := make([]string, 0, len(v))
		for _, it := range v {
			strs = append(strs, fmt.Sprint(it))
		}
		return streamFromSlices(ctx, strs), nil
	case map[string]any:
		buf, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return bytes.NewReader(buf), nil
	case <-chan []byte:
		return streamFromChanBytes(ctx, v), nil
	case chan []byte:
		return streamFromChanBytes(ctx, v), nil
	case <-chan string:
		return streamFromChanString(ctx, v), nil
	case chan string:
		return streamFromChanString(ctx, v), nil
	default:
		// best-effort string conversion
		return strings.NewReader(fmt.Sprint(v)), nil
	}
}

func streamFromSlices(ctx context.Context, chunks []string) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for _, s := range chunks {
			select {
			case <-ctx.Done():
				_ = pw.CloseWithError(ctx.Err())
				return
			default:
			}
			_, _ = io.WriteString(pw, s)
			time.Sleep(0) // yield
		}
	}()
	return pr
}

func streamFromChanBytes(ctx context.Context, ch <-chan []byte) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for {
			select {
			case <-ctx.Done():
				_ = pw.CloseWithError(ctx.Err())
				return
			case b, ok := <-ch:
				if !ok {
					return
				}
				if len(b) > 0 {
					_, _ = pw.Write(b)
				}
			}
		}
	}()
	return pr
}

func streamFromChanString(ctx context.Context, ch <-chan string) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for {
			select {
			case <-ctx.Done():
				_ = pw.CloseWithError(ctx.Err())
				return
			case s, ok := <-ch:
				if !ok {
					return
				}
				if s != "" {
					_, _ = io.WriteString(pw, s)
				}
			}
		}
	}()
	return pr
}
