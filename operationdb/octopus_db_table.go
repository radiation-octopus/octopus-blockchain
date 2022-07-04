package operationdb

import "github.com/radiation-octopus/octopus-blockchain/typedb"

type table struct {
	db     typedb.Database
	prefix string
}
