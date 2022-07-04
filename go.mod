module github.com/radiation-octopus/octopus-blockchain

go 1.15

replace github.com/radiation-octopus/octopus => ../octopus

require (
	github.com/VictoriaMetrics/fastcache v1.6.0
	github.com/VividCortex/gohistogram v1.0.0 // indirect
	github.com/deckarep/golang-set v1.8.0
	github.com/edsrzf/mmap-go v1.0.0
	github.com/ethereum/go-ethereum v1.10.19
	github.com/go-kit/kit v0.8.0
	github.com/golang/snappy v0.0.4
	github.com/google/uuid v1.2.0
	github.com/hashicorp/golang-lru v0.5.5-0.20210104140557-80c98217689d
	github.com/holiman/uint256 v1.2.0
	github.com/prometheus/tsdb v0.7.1
	github.com/radiation-octopus/octopus v0.0.0-00010101000000-000000000000
	github.com/syndtr/goleveldb v1.0.1-0.20220614013038-64ee5596c38a
	golang.org/x/crypto v0.0.0-20210921155107-089bfa567519
	gorm.io/gorm v1.23.5 // indirect
)
