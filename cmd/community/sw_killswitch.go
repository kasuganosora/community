package main

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// Service worker kill switch for rollback: FRONTEND_SERVICE_WORKER=off makes
// /sw.js a worker that deletes every Community cache and unregisters itself
// (browsers re-fetch /sw.js on navigation and on the client's update checks,
// so open and returning tabs pick it up), and marks /version.json so the
// client stops registering the worker. Unset the variable to re-enable.
const swKillScript = `/* Community service worker kill switch (FRONTEND_SERVICE_WORKER=off). */
self.addEventListener('install', () => { self.skipWaiting() })
self.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    const keys = await caches.keys()
    await Promise.all(keys.filter((k) => k.startsWith('community-')).map((k) => caches.delete(k)))
    await self.registration.unregister()
    const clients = await self.clients.matchAll({ type: 'window' })
    for (const client of clients) client.postMessage('COMMUNITY_SW_DISABLED')
  })())
})
`

// swHeader tells the client whether to keep a service worker registered.
const swHeader = "X-Community-Service-Worker"

func serviceWorkerDisabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("FRONTEND_SERVICE_WORKER")))

	return v == "off" || v == "0" || v == "false" || v == "disabled"
}

// serveSWKillSwitch answers /sw.js with the kill script when the switch is on
// and reports whether it did.
func serveSWKillSwitch(c *gin.Context, p string) bool {
	if p != "/sw.js" || !serviceWorkerDisabled() {
		return false
	}
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Service-Worker-Allowed", "/")
	c.Data(http.StatusOK, "text/javascript; charset=utf-8", []byte(swKillScript))

	return true
}
