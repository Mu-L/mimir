// SPDX-License-Identifier: AGPL-3.0-only
// Provenance-includes-location: https://github.com/cortexproject/cortex/blob/master/pkg/storegateway/gateway_http.go
// Provenance-includes-license: Apache-2.0
// Provenance-includes-copyright: The Cortex Authors.

package storegateway

import (
	_ "embed" // Used to embed html template
	"net/http"
	"text/template"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/services"
)

var (
	//go:embed ring_status.gohtml
	ringStatusPageHTML     string
	ringStatusPageTemplate = template.Must(template.New("main").Parse(ringStatusPageHTML))
)

type ringStatusPageContents struct {
	Message string
}

func writeMessage(w http.ResponseWriter, message string, logger log.Logger) {
	w.WriteHeader(http.StatusOK)
	err := ringStatusPageTemplate.Execute(w, ringStatusPageContents{Message: message})

	if err != nil {
		level.Error(logger).Log("msg", "unable to serve store gateway ring page", "err", err)
	}
}

func (g *StoreGateway) RingHandler(w http.ResponseWriter, req *http.Request) {
	if g.State() != services.Running {
		// we cannot read the ring before the store gateway is in Running state,
		// because that would lead to race condition.
		writeMessage(w, "Store gateway is not running yet.", g.logger)
		return
	}

	g.ring.ServeHTTP(w, req)
}
