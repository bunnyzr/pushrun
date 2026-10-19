// Package client renders the pushrun client shell scripts (install.sh and
// client.sh) with the server URL and version baked in (see docs/design.md).
// The scripts are the user's only touchpoint; the server serves them at
// /install.sh and /client.sh.
package client

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed install.sh
var installSh string

//go:embed client.sh
var clientSh string

var (
	installTmpl = template.Must(template.New("install.sh").Parse(installSh))
	clientTmpl  = template.Must(template.New("client.sh").Parse(clientSh))
)

type renderData struct {
	ServerURL string
	Version   string
}

// RenderInstall returns install.sh with serverURL and version baked in.
func RenderInstall(serverURL, version string) []byte {
	return render(installTmpl, renderData{serverURL, version})
}

// RenderClient returns client.sh with serverURL and version baked in.
func RenderClient(serverURL, version string) []byte {
	return render(clientTmpl, renderData{serverURL, version})
}

// render executes tmpl; the templates are parsed once at package init and a
// bytes.Buffer write cannot fail, so template execution cannot error here.
func render(tmpl *template.Template, data renderData) []byte {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		panic("client: render " + tmpl.Name() + ": " + err.Error())
	}
	return buf.Bytes()
}
