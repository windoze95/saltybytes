package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Universal links (iOS) and App Links (Android) association files. All values
// here are public by design (they also ship inside the app binaries).
//
//   - Apple team id 2M54LKDR89, bundle id codes.julian.saltybytes. The app's
//     Associated Domains entitlement lists applinks:saltybytes.ai (+ www), and
//     Apple's CDN fetches this file to let https://saltybytes.ai/r/... links
//     open the app directly. Only /r and /r/* are app-claimed — the rest of
//     the site stays in the browser.
//   - Android package codes.julian.saltybytes. Fingerprints: the Play
//     app-signing certificate (Play Console → App integrity — what
//     Play-installed builds are signed with) plus the upload key (covers
//     CI-built APKs installed directly during testing).
const appleAppSiteAssociation = `{
  "applinks": {
    "details": [
      {
        "appIDs": ["2M54LKDR89.codes.julian.saltybytes"],
        "components": [
          { "/": "/r", "comment": "share links resolved by source URL" },
          { "/": "/r/*", "comment": "public recipe pages" }
        ]
      }
    ]
  },
  "webcredentials": {
    "apps": ["2M54LKDR89.codes.julian.saltybytes"]
  }
}`

const androidAssetLinks = `[
  {
    "relation": ["delegate_permission/common.handle_all_urls"],
    "target": {
      "namespace": "android_app",
      "package_name": "codes.julian.saltybytes",
      "sha256_cert_fingerprints": [
        "6E:E1:3B:60:D1:F1:8C:A8:6A:F4:9E:81:A2:E8:EE:54:B2:44:C4:9E:12:21:77:C7:65:14:1D:0E:5A:B8:BB:51",
        "BE:77:3A:3E:13:69:07:B7:9E:2D:E8:D4:5D:AB:59:D2:17:20:C4:84:AE:C5:FF:94:00:7E:05:67:F8:5E:53:13"
      ]
    }
  }
]`

// AppleAASA serves /.well-known/apple-app-site-association. Apple requires
// application/json with no redirects.
func (h *Handler) AppleAASA(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=3600")
	c.Data(http.StatusOK, "application/json", []byte(appleAppSiteAssociation))
}

// AssetLinks serves /.well-known/assetlinks.json for Android App Links.
func (h *Handler) AssetLinks(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=3600")
	c.Data(http.StatusOK, "application/json", []byte(androidAssetLinks))
}
