package model

// UnitPort описывает порт контейнера, запущенного внутри юнита.
type UnitPort struct {
	// Container — имя контейнера без ведущего слэша.
	Container string
	// Service — значение лейбла com.docker.compose.service, если лейбл задан.
	Service string
	// InternalPort — порт внутри контейнера (ContainerPort).
	InternalPort int
	// ExternalPort — опубликованный порт хоста; 0, если порт не опубликован.
	ExternalPort int
	// Type — протокол порта: tcp или udp.
	Type string
	// IsHTTP — порт отвечает по HTTP или HTTPS.
	IsHTTP bool
}
