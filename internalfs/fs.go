package internalfs

import (
	"embed"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/utils"
	"io/fs"
	"os"
)

func Get() fs.FS {
	if config.C.DevMode == "true" { //nolint:goconst
		return os.DirFS(utils.GetFullPath("resources"))
	} else {
		return embed.FS{}
	}
}
