package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/errors"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/container"
	"github.com/docker/docker/daemon/network"
	"github.com/docker/docker/image"
	"github.com/docker/docker/opts"
	"github.com/docker/docker/pkg/signal"
	"github.com/docker/docker/pkg/system"
	"github.com/docker/docker/pkg/truncindex"
	"github.com/docker/docker/runconfig"
	"github.com/docker/go-connections/nat"
)

// GetContainer looks for a container using the provided information, which could be
// one of the following inputs from the caller:
//  - A full container ID, which will exact match a container in daemon's list
//  - A container name, which will only exact match via the GetByName() function
//  - A partial container ID prefix (e.g. short ID) of any length that is
//    unique enough to only return a single container object
//  If none of these searches succeed, an error is returned
//通过containerID或者容器名或者容器ID的前12字节来查找是否在start之前有create容器
func (daemon *Daemon) GetContainer(prefixOrName string) (*container.Container, error) {
	if len(prefixOrName) == 0 {
		return nil, errors.NewBadRequestError(fmt.Errorf("No container name or ID supplied"))
	}

	if containerByID := daemon.containers.Get(prefixOrName); containerByID != nil {
		// prefix is an exact match to a full container ID
		return containerByID, nil
	}

	// GetByName will match only an exact name provided; we ignore errors
	if containerByName, _ := daemon.GetByName(prefixOrName); containerByName != nil {
		// prefix is an exact match to a full container Name
		return containerByName, nil
	}

	containerID, indexError := daemon.idIndex.Get(prefixOrName)
	if indexError != nil {
		// When truncindex defines an error type, use that instead
		if indexError == truncindex.ErrNotExist {
			err := fmt.Errorf("No such container: %s", prefixOrName)
			return nil, errors.NewRequestNotFoundError(err)
		}
		return nil, indexError
	}
	return daemon.containers.Get(containerID), nil
}

// checkContainer make sure the specified container validates the specified conditions
func (daemon *Daemon) checkContainer(container *container.Container, conditions ...func(*container.Container) error) error {
	for _, condition := range conditions {
		if err := condition(container); err != nil {
			return err
		}
	}
	return nil
}

// Exists returns a true if a container of the specified ID or name exists,
// false otherwise.
func (daemon *Daemon) Exists(id string) bool {
	c, _ := daemon.GetContainer(id)
	return c != nil
}

// IsPaused returns a bool indicating if the specified container is paused.
func (daemon *Daemon) IsPaused(id string) bool {
	c, _ := daemon.GetContainer(id)
	return c.State.IsPaused()
}

// /var/lib/docker/container + id
func (daemon *Daemon) containerRoot(id string) string {
	return filepath.Join(daemon.repository, id)
}

// Load reads the contents of a container from disk
// This is typically done at startup.
func (daemon *Daemon) load(id string) (*container.Container, error) {
	container := daemon.newBaseContainer(id)

	if err := container.FromDisk(); err != nil {
		return nil, err
	}

	if container.ID != id {
		return container, fmt.Errorf("Container %s is stored at %s", container.ID, id)
	}

	return container, nil
}

// Register makes a container object usable by the daemon as <container.ID>
//把新建的容器信息和ID分别加入到 daemon.containers 和 daemon.idIndex
func (daemon *Daemon) Register(c *container.Container) {
	// Attach to stdout and stderr
	if c.Config.OpenStdin {
		c.StreamConfig.NewInputPipes()
	} else {
		c.StreamConfig.NewNopInputPipe()
	}

	daemon.containers.Add(c.ID, c)
	daemon.idIndex.Add(c.ID)
}

//实例化一个新的container，获取一个container实例
func (daemon *Daemon) newContainer(name string, config *containertypes.Config, hostConfig *containertypes.HostConfig, imgID image.ID, managed bool) (*container.Container, error) {
	var (
		id             string
		err            error
		noExplicitName = name == ""
	)

	//imgID为镜像的ID
	//这里的id是容器的id,也就是每个容器一个id
	id, name, err = daemon.generateIDAndName(name) //产生name:id kv对
	if err != nil {
		return nil, err
	}

	if hostConfig.NetworkMode.IsHost() { //根据id生成hostname
		if config.Hostname == "" {
			config.Hostname, err = os.Hostname()
			if err != nil {
				return nil, err
			}
		}
	} else {
		daemon.generateHostname(id, config)
	}
	entrypoint, args := daemon.getEntrypointAndArgs(config.Entrypoint, config.Cmd)

	base := daemon.newBaseContainer(id)
	base.Created = time.Now().UTC()
	base.Managed = managed
	base.Path = entrypoint
	base.Args = args //FIXME: de-duplicate from config
	base.Config = config
	base.HostConfig = &containertypes.HostConfig{}
	base.ImageID = imgID
	base.NetworkSettings = &network.Settings{IsAnonymousEndpoint: noExplicitName}
	base.Name = name
	base.Driver = daemon.GraphDriverName()

	return base, err
}

// GetByName returns a container given a name.
func (daemon *Daemon) GetByName(name string) (*container.Container, error) {
	if len(name) == 0 {
		return nil, fmt.Errorf("No container name supplied")
	}
	fullName := name
	if name[0] != '/' {
		fullName = "/" + name
	}
	id, err := daemon.nameIndex.Get(fullName)
	if err != nil {
		return nil, fmt.Errorf("Could not find entity for %s", name)
	}
	e := daemon.containers.Get(id)
	if e == nil {
		return nil, fmt.Errorf("Could not find container for entity id %s", id)
	}
	return e, nil
}

// newBaseContainer creates a new container with its initial
// configuration based on the root storage from the daemon.
func (daemon *Daemon) newBaseContainer(id string) *container.Container {
	return container.NewBaseContainer(id, daemon.containerRoot(id))
}

func (daemon *Daemon) getEntrypointAndArgs(configEntrypoint strslice.StrSlice, configCmd strslice.StrSlice) (string, []string) {
	if len(configEntrypoint) != 0 {
		return configEntrypoint[0], append(configEntrypoint[1:], configCmd...)
	}
	return configCmd[0], configCmd[1:]
}

//取id的前12字节作为hostname
func (daemon *Daemon) generateHostname(id string, config *containertypes.Config) {
	// Generate default hostname
	if config.Hostname == "" {
		config.Hostname = id[:12]
	}
}

func (daemon *Daemon) setSecurityOptions(container *container.Container, hostConfig *containertypes.HostConfig) error {
	container.Lock()
	defer container.Unlock()
	return daemon.parseSecurityOpt(container, hostConfig)
}

/*
①　daemon.registerMountPoints注册所有挂载到容器的数据卷
②　daemon.registerLinks，load所有links（包括父子关系），写入host配置至文件（ 注册互联容器，容器可以通过 ip:端口访问，可以通过--link互联。）
③　container.ToDisk将container持久化至disk。路径为如下所示
/var/lib/Docker/containers/$containerID
*/
func (daemon *Daemon) setHostConfig(container *container.Container, hostConfig *containertypes.HostConfig) error {
	// Do not lock while creating volumes since this could be calling out to external plugins
	// Don't want to block other actions, like `docker ps` because we're waiting on an external plugin

	/*
	registerMountPoints(container, hostConfig)； 注册所有挂载到容器的数据卷，主要有三种方式和来源：
	（1）容器本身原有自带的挂载的数据卷，应该是容器的json镜像文件中 "Volumes"这个key对应得内容；
	（2）通过其他数据卷容器（通过--volumes-from）挂载的数据卷;
	（3）通过命令行参数（-v参数）挂载的与主机绑定的数据卷，与主机绑定得数据卷在docker中叫做bind-mounts，这种数据卷与一般的正常得数据卷是有些细微区别的；
	*/
	if err := daemon.registerMountPoints(container, hostConfig); err != nil {
		return err
	}

	container.Lock()
	defer container.Unlock()

	/*
	RegisterLinks(container, hostConfig) (daemon/daemon_unix.go)  注册互联的容器，容器之间除了可以通过 ip:端口 相互访问，容器之
	间还可以互联（通过--link 容器名字 的方式），例如一个web容器可以通过这种方式与一个数据库容器互联；互联的容器之间可以相互访问，
	可以通过环境变量和/etc/hosts 来公开连接信息
	*/
	// Register any links from the host config before starting the container
	if err := daemon.registerLinks(container, hostConfig); err != nil {
		return err
	}

	runconfig.SetDefaultNetModeIfBlank(hostConfig)
	container.HostConfig = hostConfig
	return container.ToDisk()
}

// verifyContainerSettings performs validation of the hostconfig and config
// structures.
func (daemon *Daemon) verifyContainerSettings(hostConfig *containertypes.HostConfig, config *containertypes.Config, update bool) ([]string, error) {

	// First perform verification of settings common across all platforms.
	if config != nil {
		if config.WorkingDir != "" {
			config.WorkingDir = filepath.FromSlash(config.WorkingDir) // Ensure in platform semantics
			if !system.IsAbs(config.WorkingDir) {
				return nil, fmt.Errorf("the working directory '%s' is invalid, it needs to be an absolute path", config.WorkingDir)
			}
		}

		if len(config.StopSignal) > 0 {
			_, err := signal.ParseSignal(config.StopSignal)
			if err != nil {
				return nil, err
			}
		}

		// Validate if Env contains empty variable or not (e.g., ``, `=foo`)
		for _, env := range config.Env {
			if _, err := opts.ValidateEnv(env); err != nil {
				return nil, err
			}
		}

		// Validate the healthcheck params of Config
		if config.Healthcheck != nil {
			if config.Healthcheck.Interval != 0 && config.Healthcheck.Interval < time.Second {
				return nil, fmt.Errorf("Interval in Healthcheck cannot be less than one second")
			}

			if config.Healthcheck.Timeout != 0 && config.Healthcheck.Timeout < time.Second {
				return nil, fmt.Errorf("Timeout in Healthcheck cannot be less than one second")
			}

			if config.Healthcheck.Retries < 0 {
				return nil, fmt.Errorf("Retries in Healthcheck cannot be negative")
			}

			if config.Healthcheck.StartPeriod < 0 {
				return nil, fmt.Errorf("StartPeriod in Healthcheck cannot be negative")
			}
		}
	}

	if hostConfig == nil {
		return nil, nil
	}

	if hostConfig.AutoRemove && !hostConfig.RestartPolicy.IsNone() {
		return nil, fmt.Errorf("can't create 'AutoRemove' container with restart policy")
	}

	for _, extraHost := range hostConfig.ExtraHosts {
		if _, err := opts.ValidateExtraHost(extraHost); err != nil {
			return nil, err
		}
	}

	for port := range hostConfig.PortBindings {
		_, portStr := nat.SplitProtoPort(string(port))
		if _, err := nat.ParsePort(portStr); err != nil {
			return nil, fmt.Errorf("invalid port specification: %q", portStr)
		}
		for _, pb := range hostConfig.PortBindings[port] {
			_, err := nat.NewPort(nat.SplitProtoPort(pb.HostPort))
			if err != nil {
				return nil, fmt.Errorf("invalid port specification: %q", pb.HostPort)
			}
		}
	}

	p := hostConfig.RestartPolicy

	switch p.Name {
	case "always", "unless-stopped", "no":
		if p.MaximumRetryCount != 0 {
			return nil, fmt.Errorf("maximum retry count cannot be used with restart policy '%s'", p.Name)
		}
	case "on-failure":
		if p.MaximumRetryCount < 0 {
			return nil, fmt.Errorf("maximum retry count cannot be negative")
		}
	case "":
	// do nothing
	default:
		return nil, fmt.Errorf("invalid restart policy '%s'", p.Name)
	}

	// Now do platform-specific verification
	return verifyPlatformContainerSettings(daemon, hostConfig, config, update)
}
