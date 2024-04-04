package inu

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"inu/cid"
	"inu/dht"
	"inu/fs"
	"inu/store"
)

func TestAPI_Add_Cat(t *testing.T) {
	config := DefaultDaemonConfig()
	config.StorePath = store.InMemory
	config.PublicIP = "dummy"

	d := NewDaemon(config)
	d.Start()
	defer d.Stop()
	time.Sleep(1 * time.Second)

	api, err := NewAPIFromConfig(config)
	require.NoError(t, err)

	t.Run("add path", func(t *testing.T) {
		rs, err := api.AddPath("test/redcat.jpg")
		require.NoError(t, err)
		require.Len(t, rs, 1)
		require.Equal(t, fs.Record{
			CID:  "QQ7OAXJ25MYFACNNFTQQBZMSNNO4PMQA5NVYATGYQETALXUTSJ3Q",
			Path: "test/redcat.jpg",
		}, rs[0])
	})

	bytesToAdd := []byte{byte('a')}
	t.Run("add bytes", func(t *testing.T) {
		id, err := api.AddBytes(bytesToAdd)
		require.NoError(t, err)
		require.Equal(t, cid.CID("AHBHO3TJNH4OGKMXABIXZVDVAHNE5OYDIW4NLUO744U6U26SWYKQ"), id)
	})

	t.Run("cat - add path", func(t *testing.T) {
		t.Run("add path", func(t *testing.T) {
			fileData, err := os.ReadFile("test/redcat.jpg")
			require.NoError(t, err)
			catData, err := api.Cat("QQ7OAXJ25MYFACNNFTQQBZMSNNO4PMQA5NVYATGYQETALXUTSJ3Q")
			require.NoError(t, err)
			require.Equal(t, fileData, catData)
		})

		t.Run("add bytes", func(t *testing.T) {
			catData, err := api.Cat("AHBHO3TJNH4OGKMXABIXZVDVAHNE5OYDIW4NLUO744U6U26SWYKQ")
			require.NoError(t, err)
			require.Equal(t, bytesToAdd, catData)
		})
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
			CID:  "QQ7OAXJ25MYFACNNFTQQBZMSNNO4PMQA5NVYATGYQETALXUTSJ3Q",
			Path: "test/redcat.jpg",
		}, rs[0])

		// Now upload it
		count, err := api.Upload("QQ7OAXJ25MYFACNNFTQQBZMSNNO4PMQA5NVYATGYQETALXUTSJ3Q")
		require.NoError(t, err)
		require.Equal(t, uint(6), count)
	})

	t.Run("download", func(t *testing.T) {
		api := createAPI(t, dmnB.config.Port)

		// Download the DAG
		require.NoError(t, api.Download("QQ7OAXJ25MYFACNNFTQQBZMSNNO4PMQA5NVYATGYQETALXUTSJ3Q"))

		// Verify content stored correctly
		s, err := dmnB.store.Size()
		require.NoError(t, err)
		require.Equal(t, uint(6), s)
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
	c := DefaultDaemonConfig()
	c.StorePath = store.InMemory
	c.Port = port
	c.RpcPort = port + 1000
	c.PublicIP = "dummy"
	return NewDaemon(c)
}

func createAPI(t *testing.T, port uint16) *API {
	c := DefaultDaemonConfig()
	c.Port = port
	c.RpcPort = port + 1000
	c.PublicIP = "dummy"
	api, err := NewAPIFromConfig(c)
	require.NoError(t, err)
	return api
}
