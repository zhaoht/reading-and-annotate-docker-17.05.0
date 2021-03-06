// +build solaris linux freebsd

package config

import (
	"net"

	"github.com/docker/docker/api/types"
)

// CommonUnixConfig defines configuration of a docker daemon that is
// common across Unix platforms.
//包含在config\config_unix.go中的 Config 结构中
// Config 包含 CommonConfig(unix  windos都包含的共用配置)  CommonUnixConfig(unix系统特有的配置)
type CommonUnixConfig struct {
	//默认/run/docker/libcontainerd
	//--exec-root string                      Root directory for execution state files (default "/var/run/docker")
	ExecRoot          string                   `json:"exec-root,omitempty"`
	//--containerd string                     Path to containerd socket
	ContainerdAddr    string                   `json:"containerd,omitempty"`

    LxcfsAutoStart    bool 			   `json:"lxcfs-autostart,omitempty"`
	LxcfsAddr         string                   `json:"lxcfs-addr,omitempty"`
	LxcfsDebug        bool                     `json:"lxcfs-debug,omitempty"`
	LxcfsLogPath      string                   `json:"lxcfs-log-path,omitempty"`
	LxcfsOffMultithread  bool                `json:"lxcfs-off-multithread,omitempty"`
	LxcfsAllowOther   bool                   `json:"lxcfs-allow-other,omitempty"`
	LxcfsMountPath     string                   `json:"lxcfs-mount-path,omitempty"`
	
	//runc成员赋值见 verifyDaemonSettings
	Runtimes          map[string]types.Runtime `json:"runtimes,omitempty"`
	//赋值见verifyDaemonSettings
	DefaultRuntime    string                   `json:"default-runtime,omitempty"`
	DefaultInitBinary string                   `json:"default-init,omitempty"`
}

type commonUnixBridgeConfig struct {
	//绑定容器端口时使用的默认IP
	DefaultIP                   net.IP `json:"ip,omitempty"`
	IP                          string `json:"bip,omitempty"`
	DefaultGatewayIPv4          net.IP `json:"default-gateway,omitempty"`
	DefaultGatewayIPv6          net.IP `json:"default-gateway-v6,omitempty"`
	//是否允许宿主机上docker容器见通信  InterContainerCommunication的作用是启用Docker container之间互相通信的功能
	InterContainerCommunication bool   `json:"icc,omitempty"`
}

// GetRuntime returns the runtime path and arguments for a given
// runtime name
func (conf *Config) GetRuntime(name string) *types.Runtime {
	conf.Lock()
	defer conf.Unlock()
	if rt, ok := conf.Runtimes[name]; ok {
		return &rt
	}
	return nil
}

// GetDefaultRuntimeName returns the current default runtime
func (conf *Config) GetDefaultRuntimeName() string {
	conf.Lock()
	rt := conf.DefaultRuntime
	conf.Unlock()

	return rt
}

// GetAllRuntimes returns a copy of the runtimes map
func (conf *Config) GetAllRuntimes() map[string]types.Runtime {
	conf.Lock()
	rts := conf.Runtimes
	conf.Unlock()
	return rts
}

// GetExecRoot returns the user configured Exec-root
func (conf *Config) GetExecRoot() string {
	return conf.ExecRoot
}

// GetInitPath returns the configure docker-init path
func (conf *Config) GetInitPath() string {
	conf.Lock()
	defer conf.Unlock()
	if conf.InitPath != "" {
		return conf.InitPath
	}
	if conf.DefaultInitBinary != "" {
		return conf.DefaultInitBinary
	}
	return DefaultInitBinary
}
