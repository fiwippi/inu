package inu

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"inu/dht"
	"inu/fs"
	"inu/store"
)

func TestAPI_AddPath_Cat(t *testing.T) {
	config := dht.DefaultClientConfig()

	d := NewDaemon(store.InMemory, config)
	d.Start()
	defer d.Stop()
	time.Sleep(1 * time.Second)

	api, err := NewAPI(config)
	require.NoError(t, err)

	t.Run("add path", func(t *testing.T) {
		rs, err := api.AddPath("test/redcat.jpg")
		require.NoError(t, err)
		require.Len(t, rs, 1)
		require.Equal(t, fs.Record{
			CID:  "JQXCTSC5JZCGXBGYVRXTZPBWLP6JCYZTBIDSCYEGZJCUKHTYOINA",
			Path: "test/redcat.jpg",
		}, rs[0])
	})

	t.Run("cat", func(t *testing.T) {
		fileData, err := os.ReadFile("test/redcat.jpg")
		require.NoError(t, err)
		catData, err := api.Cat("JQXCTSC5JZCGXBGYVRXTZPBWLP6JCYZTBIDSCYEGZJCUKHTYOINA")
		require.NoError(t, err)
		require.Equal(t, fileData, catData)
	})
}

func TestAPI_Upload_Download(t *testing.T) {
	// Start 2 DHT nodes
	dhtA := createDHT(t, 0, 3000)
	dhtB := createDHT(t, 1, 3001)

	require.NoError(t, dhtA.Start())
	defer dhtA.Stop()
	time.Sleep(1 * time.Second)
	require.NoError(t, dhtB.Start())
	defer dhtB.Stop()
	time.Sleep(1 * time.Second)
	require.NoError(t, dhtB.Bootstrap(dhtA.Contact()))

	// Start two daemons
	dmnA := createDaemon(6000)
	dmnB := createDaemon(6001)

	dmnA.Start()
	defer dmnA.Stop()
	dmnB.Start()
	defer dmnB.Stop()
	time.Sleep(1 * time.Second)

	// Now we upload a file from daemon A
	// and download it from daemon B

	t.Run("upload", func(t *testing.T) {
		api := createAPI(t, dmnA.config.Port)

		// First add it
		rs, err := api.AddPath("test/redcat.jpg")
		require.NoError(t, err)
		require.Len(t, rs, 1)
		require.Equal(t, fs.Record{
			CID:  "JQXCTSC5JZCGXBGYVRXTZPBWLP6JCYZTBIDSCYEGZJCUKHTYOINA",
			Path: "test/redcat.jpg",
		}, rs[0])

		// Now upload it
		count, err := api.Upload("JQXCTSC5JZCGXBGYVRXTZPBWLP6JCYZTBIDSCYEGZJCUKHTYOINA")
		require.NoError(t, err)
		require.Equal(t, uint(11), count)
	})

	t.Run("download", func(t *testing.T) {
		api := createAPI(t, dmnB.config.Port)

		// Download the DAG
		require.NoError(t, api.Download("JQXCTSC5JZCGXBGYVRXTZPBWLP6JCYZTBIDSCYEGZJCUKHTYOINA"))

		// Verify content stored correctly
		s, err := dmnB.store.Size()
		require.NoError(t, err)
		require.Equal(t, uint(11), s)
	})
}

func createDHT(t *testing.T, k uint64, port uint16) *dht.Node {
	c := dht.DefaultNodeConfig()
	c.ID = dht.ParseUint64(k)
	c.Port = port
	n, err := dht.NewNode(c)
	require.NoError(t, err)
	return n
}

func createDaemon(port uint16) *Daemon {
	c := dht.DefaultClientConfig()
	c.Port = port
	return NewDaemon(store.InMemory, c)
}

func createAPI(t *testing.T, port uint16) *API {
	c := dht.DefaultClientConfig()
	c.Port = port
	api, err := NewAPI(c)
	require.NoError(t, err)
	return api
}
