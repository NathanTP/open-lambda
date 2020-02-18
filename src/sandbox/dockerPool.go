package sandbox

import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"syscall"

	docker "github.com/fsouza/go-dockerclient"
	"github.com/jaypipes/ghw"

	"github.com/open-lambda/open-lambda/ol/common"
	"github.com/open-lambda/open-lambda/ol/sandbox/dockerutil"
)

// DockerPool is a ContainerFactory that creats docker containers.
type DockerPool struct {
	client         *docker.Client
	labels         map[string]string
	caps           []string
	pidMode        string
	pkgsDir        string
	idxPtr         *int64
	docker_runtime string
	eventHandlers  []SandboxEventFunc
	debugger
}

// NewDockerPool creates a DockerPool.
func NewDockerPool(pidMode string, caps []string) (*DockerPool, error) {
	client, err := docker.NewClientFromEnv()
	if err != nil {
		return nil, err
	}

	var sharedIdx int64 = -1
	idxPtr := &sharedIdx

	labels := map[string]string{
		dockerutil.DOCKER_LABEL_CLUSTER: common.Conf.Worker_dir,
	}

	pool := &DockerPool{
		client:         client,
		labels:         labels,
		caps:           caps,
		pidMode:        pidMode,
		pkgsDir:        common.Conf.Pkgs_dir,
		idxPtr:         idxPtr,
		docker_runtime: common.Conf.Docker_runtime,
		eventHandlers:  []SandboxEventFunc{},
	}

	pool.debugger = newDebugger(pool)

	return pool, nil
}

// Create creates a docker sandbox from the handler and sandbox directory.
func (pool *DockerPool) Create(parent Sandbox, isLeaf bool, codeDir, scratchDir string, meta *SandboxMeta) (sb Sandbox, err error) {
	meta = fillMetaDefaults(meta)
	t := common.T0("Create()")
	defer t.T1()

	if parent != nil {
		panic("Create parent not supported for DockerPool")
	} else if !isLeaf {
		panic("Non-leaves not supported for DockerPool")
	}

	id := fmt.Sprintf("%d", atomic.AddInt64(pool.idxPtr, 1))

	volumes := []string{
		fmt.Sprintf("%s:%s:z", scratchDir, "/host"),
		fmt.Sprintf("%s:%s:z,ro", pool.pkgsDir, "/packages"),
	}

	if codeDir != "" {
		volumes = append(volumes, fmt.Sprintf("%s:%s:z,ro", codeDir, "/handler"))
	}

	// pipe for synchronization before socket is ready
	pipe := filepath.Join(scratchDir, "server_pipe")
	if err := syscall.Mkfifo(pipe, 0777); err != nil {
		return nil, err
	}

	var deviceReq []docker.DeviceRequest
	if common.Conf.Features.Enable_gpu {
		deviceReq = []docker.DeviceRequest{{
			Driver:       "nvidia",
			Count:        1,
			Capabilities: [][]string{{"gpu"}},
		}}
	} else {
		deviceReq = []docker.DeviceRequest{}
	}

	container, err := pool.client.CreateContainer(
		docker.CreateContainerOptions{
			Config: &docker.Config{
				Cmd:    []string{"/spin"},
				Image:  dockerutil.LAMBDA_IMAGE,
				Labels: pool.labels,
			},
			HostConfig: &docker.HostConfig{
				Binds:          volumes,
				CapAdd:         pool.caps,
				PidMode:        pool.pidMode,
				Runtime:        pool.docker_runtime,
				DeviceRequests: deviceReq,
			},
		},
	)
	if err != nil {
		return nil, err
	}

	c := &DockerContainer{
		host_id:   id,
		hostDir:   scratchDir,
		container: container,
		client:    pool.client,
		installed: make(map[string]bool),
		meta:      meta,
	}

	if err := c.start(); err != nil {
		c.Destroy()
		return nil, err
	}

	if err := c.runServer(); err != nil {
		c.Destroy()
		return nil, err
	}

	if err := waitForServerPipeReady(c.HostDir()); err != nil {
		c.Destroy()
		return nil, err
	}

	// wrap to make thread-safe and handle container death
	safe := newSafeSandbox(c)
	safe.startNotifyingListeners(pool.eventHandlers)
	return safe, nil
}

func (pool *DockerPool) Cleanup() {}

func (pool *DockerPool) DebugString() string {
	return pool.debugger.Dump()
}

func (pool *DockerPool) AddListener(handler SandboxEventFunc) {
	pool.eventHandlers = append(pool.eventHandlers, handler)
}

// Return the maximum concurrency supported by this pool or -1 if there is no
// limit
func (pool *DockerPool) MaxConcurrency() (int, error) {
	if common.Conf.Features.Enable_gpu {
		gpuInfo, err := ghw.GPU()
		if err != nil {
			fmt.Println("Failed to detect GPUs")
			return 1, err
		}
		var ngpu int = 0
		for _, card := range gpuInfo.GraphicsCards {
			//This is not the right way to do this, I should figure out how to
			//determine if the card supports CUDA. This works for now though.
			if card.DeviceInfo.Vendor.Name == "NVIDIA Corporation" {
				fmt.Printf("WARNING: assuming card %v supports CUDA\n", card.DeviceInfo.Product.Name)
				ngpu++
			}
		}
		fmt.Println("DockerPool setting instance limit to the number of GPUs: ", ngpu)
		return ngpu, nil
	} else {
		return -1, nil
	}
}
