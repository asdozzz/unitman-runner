package config

import "strings"

// Переменные релизной сборки. Значения подставляются линкером:
//
//	go build -ldflags "-X runner/config.BuildVersion=v1.2.3 \
//	  -X runner/config.BuildCommit=<sha> \
//	  -X runner/config.BuildDate=<RFC3339>"
var (
	BuildVersion = "dev"
	BuildCommit  = "unknown"
	BuildDate    = "unknown"

	// UnitImageRepository и IdeImageRepository — репозитории образов юнита и IDE
	// внутри реестра установки. Тег задаётся UnitImageTag, а при пустом
	// значении берётся BuildVersion.
	UnitImageRepository = "unitman/unit"
	IdeImageRepository  = "unitman/ide"
	UnitImageTag        = ""

	// UpdateRepository — OCI-образ релиза, из которого `runner update` берёт
	// новый бинарь. Подставляется линкером при релизной сборке.
	UpdateRepository = "ghcr.io/owner/unitman-release"
)

// UnitImageRef возвращает ссылку на образ юнита для реестра установки.
func (c Configuration) UnitImageRef() string {
	return c.imageRef(UnitImageRepository)
}

// IdeImageRef возвращает ссылку на образ IDE для реестра установки.
func (c Configuration) IdeImageRef() string {
	return c.imageRef(IdeImageRepository)
}

func (c Configuration) imageRef(repository string) string {
	tag := strings.TrimSpace(UnitImageTag)
	if tag == "" {
		tag = BuildVersion
	}

	host := strings.TrimRight(strings.TrimSpace(c.RegistryHost), "/")
	repo := strings.TrimLeft(strings.TrimSpace(repository), "/")

	return host + "/" + repo + ":" + tag
}
