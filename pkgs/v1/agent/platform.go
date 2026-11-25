package agent

import (
	"net"
	"os"
	"runtime"

	"github.com/tguidoux/openssm/pkgs/v1/agentproto"
)

// Version is the agent version reported to the server.
var Version = "0.1.0"

// PlatformType maps GOOS to the SSM platform type.
func PlatformType() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "MacOS"
	default:
		return "Linux"
	}
}

// HostInfo collects host information reported on registration and heartbeat.
func HostInfo() agentproto.AgentInfo {
	host, _ := os.Hostname()
	return agentproto.AgentInfo{
		ComputerName:    host,
		IPAddress:       primaryIP(),
		PlatformType:    PlatformType(),
		PlatformName:    runtime.GOOS,
		PlatformVersion: runtime.GOARCH,
		AgentVersion:    Version,
	}
}

func primaryIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return ""
}
