package weixin

import (
	"fmt"
	"io"

	"github.com/mdp/qrterminal/v3"
)

// qrFallbackNotice is printed under every QR code. The link is a complete
// alternative path, so it is shown even when rendering succeeds.
const qrFallbackNotice = "若二维码未能显示或无法使用，你可以访问以下链接以继续："

// DisplayQRCode renders a login URL as a terminal QR code and always prints the
// raw URL underneath. Rendering failures are not fatal: the link alone is
// enough to complete the login.
func DisplayQRCode(w io.Writer, qrcodeURL string) {
	if qrcodeURL == "" {
		return
	}
	func() {
		defer func() {
			// qrterminal panics on input it cannot encode; the URL fallback below
			// still lets the operator finish.
			_ = recover()
		}()
		qrterminal.GenerateHalfBlock(qrcodeURL, qrterminal.L, w)
	}()
	fmt.Fprintf(w, "%s\n%s\n", qrFallbackNotice, qrcodeURL)
}
