package store

import (
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"inu"
)

const InMemory string = "file::memory:"

type Store struct {
	pool *sqlx.DB
}

func NewStore(path string) *Store {
	// Initialise the store
	s := &Store{
		pool: sqlx.MustConnect("sqlite", path+"?_pragma=foreign_keys(on)"),
	}

	// Create the tables
	s.pool.MustExec(
		`CREATE TABLE IF NOT EXISTS blocks (
         cid  TEXT PRIMARY KEY,
         data BLOB NOT NULL 
	     );`)

	return s
}

func (s *Store) Close() error {
	return s.pool.Close()
}

func (s *Store) Get(cid inu.CID) (Block, error) {
	var b Block
	return b, s.pool.Get(&b, `SELECT cid, data FROM blocks WHERE cid = ? LIMIT 1`, cid)
}

func (s *Store) Put(b Block) error {
	_, err := s.pool.Exec(`REPLACE INTO blocks (cid, data) VALUES (?, ?)`, b.CID, b.Data)
	return err
}

func (s *Store) Delete(cid inu.CID) error {
	_, err := s.pool.Exec(`DELETE FROM blocks WHERE cid = ?`, cid)
	return err
}
func (s *Store) Size() (uint, error) {
	var n uint
	return n, s.pool.Get(&n, `SELECT COUNT(*) FROM blocks`)
}
