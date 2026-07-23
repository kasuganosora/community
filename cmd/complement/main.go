package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/matrix/homeserver"
	"github.com/kasuganosora/community/models"
)

// Complement-specific entry point for Matrix homeserver testing.
// Environment variables (set by Complement or Dockerfile):
//   - SERVER_NAME: homeserver identity (e.g. "hs1")
//   - DB_CONNECTION=sqlite, DB_HOST=/data/community.db
//   - TLS_CERT, TLS_KEY: paths to TLS cert/key for federation port 8448

func main() {
	serverName := os.Getenv("SERVER_NAME")
	if serverName == "" {
		serverName = "localhost"
	}

	tlsCert := os.Getenv("TLS_CERT")
	tlsKey := os.Getenv("TLS_KEY")

	// Auto-migrate community core tables needed for auth
	err := db.GetDB().AutoMigrate(
		&models.User{},
		&models.Account{},
		&models.AccountExtra{},
		&models.UserSecretData{},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: core table migration failed: %v\n", err)
	}

	// Init homeserver (creates all Matrix tables via autoMigrate)
	err = homeserver.InitHomeServer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Homeserver init failed: %v\n", err)
		os.Exit(1)
	}

	gin.SetMode(gin.ReleaseMode)

	app := gin.New()
	app.Use(gin.Recovery())
	// Register all Matrix routes
	homeserver.RegisterRoutes(app)

	// Register Federation API routes
	homeserver.RegisterFederationRoutes(app)
	homeserver.RegisterBackfillRoutes(app)
	homeserver.RegisterAdditionalFederationRoutes(app)
	homeserver.RegisterFederationE2EERoutes(app)

	// Register Client-Server API extension routes (not in RegisterRoutes)
	homeserver.RegisterRelationsRoutes(app)
	homeserver.RegisterSpacesRoutes(app)
	homeserver.RegisterAppServiceRoutes(app)
	homeserver.RegisterRoomUpgradeRoutes(app)
	homeserver.RegisterKeySharingRoutes(app)
	homeserver.RegisterBatchSendRoutes(app)
	homeserver.RegisterAsyncMediaRoutes(app)

	// Start HTTPS on 8448 for federation
	if tlsCert != "" && tlsKey != "" {
		go func() {
			srv := &http.Server{ //nolint:gosec // G112: reviewed and safe
				Addr:      ":8448",
				Handler:   app,
				TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			}

			fmt.Printf("Federation HTTPS listening on :8448 (SERVER_NAME=%s)\n", serverName)

			err := srv.ListenAndServeTLS(tlsCert, tlsKey)

			if err != nil {
				fmt.Fprintf(os.Stderr, "HTTPS server error: %v\n", err)
			}
		}()
	}

	// Start HTTP on 8008 for client API
	fmt.Printf("Client HTTP listening on :8008 (SERVER_NAME=%s)\n", serverName)

	err = app.Run(":8008")

	if err != nil {
		fmt.Fprintf(os.Stderr, "HTTP server error: %v\n", err)
		os.Exit(1)
	}
}
