package shot

import (
	"fmt"

	"github.com/chromedp/cdproto/runtime"
)

func sprintf(f string, a ...any) string { return fmt.Sprintf(f, a...) }

func awaitPromise(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }
