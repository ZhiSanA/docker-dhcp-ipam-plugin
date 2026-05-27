package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/docker/go-plugins-helpers/sdk"

	"github.com/tuzi/docker-dhcp-ipam-plugin/pkg/config"
	"github.com/tuzi/docker-dhcp-ipam-plugin/pkg/dhcp"
	"github.com/tuzi/docker-dhcp-ipam-plugin/pkg/iface"
	ipamdriver "github.com/tuzi/docker-dhcp-ipam-plugin/pkg/ipam"
	"github.com/tuzi/docker-dhcp-ipam-plugin/pkg/store"

	iptypes "github.com/docker/go-plugins-helpers/ipam"
)

// lowerCaseManifest uses lowercase for Docker 29.x plugin capability matching.
const lowerCaseManifest = `{"Implements":["ipamdriver"]}`

// ipamHandler wraps sdk.Handler with routes for the IPAM driver API.
type ipamHandler struct {
	sdk.Handler
}

func newIPAMHandler(driver *ipamdriver.Driver) *ipamHandler {
	h := &ipamHandler{
		Handler: sdk.NewHandler(lowerCaseManifest),
	}
	h.registerRoutes(driver)
	return h
}

func (h *ipamHandler) registerRoutes(driver *ipamdriver.Driver) {
	h.HandleFunc("/IpamDriver.GetCapabilities", func(w http.ResponseWriter, r *http.Request) {
		res, err := driver.GetCapabilities()
		if err != nil {
			encodeErr(w, err)
			return
		}
		json.NewEncoder(w).Encode(res)
	})
	h.HandleFunc("/IpamDriver.GetDefaultAddressSpaces", func(w http.ResponseWriter, r *http.Request) {
		res, err := driver.GetDefaultAddressSpaces()
		if err != nil {
			encodeErr(w, err)
			return
		}
		json.NewEncoder(w).Encode(res)
	})
	h.HandleFunc("/IpamDriver.RequestPool", func(w http.ResponseWriter, r *http.Request) {
		req := &struct {
			AddressSpace string `json:"AddressSpace"`
			Pool         string `json:"Pool"`
			SubPool      string `json:"SubPool"`
			Options      map[string]string `json:"Options"`
			V6           bool   `json:"V6"`
		}{}
		if err := json.NewDecoder(r.Body).Decode(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		res, err := driver.RequestPool(&iptypes.RequestPoolRequest{
			AddressSpace: req.AddressSpace,
			Pool:         req.Pool,
			SubPool:      req.SubPool,
			Options:      req.Options,
			V6:           req.V6,
		})
		if err != nil {
			encodeErr(w, err)
			return
		}
		json.NewEncoder(w).Encode(res)
	})
	h.HandleFunc("/IpamDriver.ReleasePool", func(w http.ResponseWriter, r *http.Request) {
		req := &struct {
			PoolID string `json:"PoolID"`
		}{}
		if err := json.NewDecoder(r.Body).Decode(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := driver.ReleasePool(&iptypes.ReleasePoolRequest{PoolID: req.PoolID}); err != nil {
			encodeErr(w, err)
			return
		}
		json.NewEncoder(w).Encode(struct{}{})
	})
	h.HandleFunc("/IpamDriver.RequestAddress", func(w http.ResponseWriter, r *http.Request) {
		req := &struct {
			PoolID  string            `json:"PoolID"`
			Address string            `json:"Address"`
			Options map[string]string `json:"Options"`
		}{}
		if err := json.NewDecoder(r.Body).Decode(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		res, err := driver.RequestAddress(&iptypes.RequestAddressRequest{
			PoolID:  req.PoolID,
			Address: req.Address,
			Options: req.Options,
		})
		if err != nil {
			encodeErr(w, err)
			return
		}
		json.NewEncoder(w).Encode(res)
	})
	h.HandleFunc("/IpamDriver.ReleaseAddress", func(w http.ResponseWriter, r *http.Request) {
		req := &struct {
			PoolID  string `json:"PoolID"`
			Address string `json:"Address"`
		}{}
		if err := json.NewDecoder(r.Body).Decode(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := driver.ReleaseAddress(&iptypes.ReleaseAddressRequest{
			PoolID:  req.PoolID,
			Address: req.Address,
		}); err != nil {
			encodeErr(w, err)
			return
		}
		json.NewEncoder(w).Encode(struct{}{})
	})
}

func encodeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", sdk.DefaultContentTypeV1_1)
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(map[string]string{"Err": err.Error()})
}

func main() {
	cfg := config.Load()
	log.Printf("starting DHCP IPAM plugin, interface=%s, socket=%s",
		cfg.HostInterface, cfg.SocketPath)

	ifaceInfo, err := iface.Detect(cfg.HostInterface)
	if err != nil {
		log.Fatalf("failed to detect host interface: %v", err)
	}
	log.Printf("detected interface: %s (MAC=%s, subnet=%s, gateway=%s)",
		ifaceInfo.InterfaceName, ifaceInfo.HardwareAddr, ifaceInfo.Subnet, ifaceInfo.Gateway)

	if cfg.HostInterface == "" {
		cfg.HostInterface = ifaceInfo.InterfaceName
	}

	driver := ipamdriver.New(cfg, ifaceInfo, dhcp.NewClient(cfg), store.NewPoolStore(), store.NewLeaseStore())
	handler := newIPAMHandler(driver)

	os.Remove(cfg.SocketPath)
	log.Printf("listening on %s", cfg.SocketPath)
	if err := handler.ServeUnix(cfg.SocketPath, os.Getgid()); err != nil {
		log.Fatalf("failed to serve on %s: %v", cfg.SocketPath, err)
	}
}
