package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	mcpfrontend "github.com/east-true/opcda-access-adapter/internal/frontend/mcp"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// RunMCP는 MCP가 명시적으로 선택된 경우에만 stdio와 DA Runtime을 실행한다.
func RunMCP(ctx context.Context, config Config, runtime opcda.Runtime, input io.ReadCloser, output io.WriteCloser, version string) error {
	// 1. 다른 프런트엔드의 입출력이나 런타임에는 접근하지 않는다.
	if err := config.finalizeAndValidate(); err != nil {
		return err
	}
	if config.Frontend != FrontendMCP {
		return fmt.Errorf("MCP is disabled unless frontend mcp is explicitly selected")
	}
	if runtime == nil {
		var err error
		runtime, err = opcda.New(config.Runtime)
		if err != nil {
			return err
		}
	}

	// 2. MCP 세션과 그룹 정리가 끝난 뒤 소스 런타임도 반드시 종료한다.
	server, err := mcpfrontend.New(runtime, mcpfrontend.Config{Limits: config.Runtime.Limits, RequestDeadline: config.RequestDeadline, Version: version})
	if err == nil {
		err = server.Run(ctx, input, output)
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), startupCleanupTimeout)
	defer cancel()
	return errors.Join(err, runtime.Shutdown(cleanup))
}
