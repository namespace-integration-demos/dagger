package drivers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	computepb "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/compute/v1beta"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"google.golang.org/protobuf/types/known/timestamppb"
	"namespacelabs.dev/integrations/errors/multierr"
	"namespacelabs.dev/integrations/nsc/auth"
	"namespacelabs.dev/integrations/nsc/compute"
	"namespacelabs.dev/integrations/nsc/ingress"
)

func init() {
	register("namespace", &namespaceDriver{})
}

type namespaceDriver struct{}

type namespaceConnector struct {
	cli       compute.Client
	md        *computepb.InstanceMetadata
	autoclean bool

	mu       sync.Mutex
	refcount int // A simple ref count to automatically terminate the instance after use.
}

func (drv namespaceDriver) Provision(ctx context.Context, url *url.URL, opts *DriverOpts) (Connector, error) {
	token, err := auth.LoadUserToken()
	if err != nil {
		return nil, err
	}

	name := strings.TrimPrefix(url.Path, "/")

	shape := &computepb.InstanceShape{
		VirtualCpu:      8,
		MemoryMegabytes: 16 * 1024,
		MachineArch:     "amd64", // Can also do "arm64".
	}

	if rawShape := url.Query().Get("shape"); rawShape != "" {
		cpu, mem, err := parseShape(rawShape)
		if err != nil {
			return nil, err
		}

		shape.VirtualCpu = cpu
		shape.MemoryMegabytes = mem * 1024
	}

	if arch := url.Query().Get("arch"); arch != "" {
		shape.MachineArch = arch
	}

	duration := time.Hour
	if ttl := url.Query().Get("ttl"); ttl != "" {
		dur, err := time.ParseDuration(ttl)
		if err != nil {
			return nil, err
		}

		duration = dur
	}

	autoclean := len(name) == 0
	if arg := url.Query().Get("autoclean"); arg != "" {
		autoclean, _ = strconv.ParseBool(arg)
	}

	// Create a stub to use the Namespace Compute API.
	cli, err := compute.NewClient(ctx, token)
	if err != nil {
		return nil, err
	}

	ver := engine.Version
	if ver == "" {
		ver = "0.11.4"
	}

	req := &computepb.CreateInstanceRequest{
		Shape:             shape,
		DocumentedPurpose: fmt.Sprintf("Dagger engine %s", ver),
		// Block until resources for the instance have been allocated.
		Interactive: true,
		Deadline:    timestamppb.New(time.Now().Add(duration)),
		// Run the engine in a container.
		Containers: []*computepb.ContainerRequest{{
			Name:     "dagger-engine",
			ImageRef: fmt.Sprintf("%s:v%s", engine.EngineImageRepo, ver),
			Args:     []string{},
			// It's privileged, that's OK, as the instance provides the isolation.
			Privileged: true,
			// Make Docker available to the container under /var/run/docker.sock
			DockerSockPath: "/var/run/docker.sock",
			// Forward Namespace credentials to the container.
			NscStatePath: "/var/run/nsc",
			Experimental: &computepb.ContainerRequest_ExperimentalFeatures{
				ExportedUnixSockets: map[string]string{
					"dagger-buildkit": "/run/buildkit/buildkitd.sock",
				},
			},
		}},
	}

	if len(name) > 0 {
		// Attach a cache volume, and mount it to /var/lib/dagger.
		req.Containers[0].Volumes = append(req.Containers[0].Volumes, &computepb.VolumeRequest{
			// This is the unique name of the cache volume, that will lead to re-use.
			Tag:             maybeSuffix("dagger-cache", name),
			MountPoint:      "/var/lib/dagger",
			SizeMb:          64 * 1024,
			PersistencyKind: computepb.VolumeRequest_CACHE,
		})

		req.Experimental = &computepb.CreateInstanceRequest_ExperimentalFeatures{
			// Attach a unique tag to the instance, which will lead to automatic re-use.
			UniqueTag: maybeSuffix("dagger-engine", name),
		}
	}

	// Create or re-use an existing instance that runs the dagger engine.
	resp, err := cli.Compute.CreateInstance(ctx, req)
	if err != nil {
		_ = cli.Conn.Close()
		return nil, err
	}

	slog.Info("[namespace] created instance",
		"instance_id", resp.Metadata.InstanceId, "url", resp.InstanceUrl)

	// Wait until the instance is ready.
	md, err := cli.Compute.WaitInstanceSync(ctx, &computepb.WaitInstanceRequest{
		InstanceId: resp.Metadata.InstanceId,
	})
	if err != nil {
		_ = cli.Conn.Close()
		return nil, err
	}

	return &namespaceConnector{cli: cli, md: md.Metadata, autoclean: autoclean}, nil
}

func (conn *namespaceConnector) Connect(ctx context.Context) (net.Conn, error) {
	token, err := auth.LoadUserToken()
	if err != nil {
		return nil, err
	}

	c, err := ingress.DialNamedUnixSocket(ctx, io.Discard, token, conn.md, "dagger-buildkit")
	if err != nil {
		return nil, err
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	conn.refcount++

	return namespaceConnWrapper{c, conn}, nil
}

func (conn *namespaceConnector) deref() bool {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	conn.refcount--
	return conn.refcount == 0
}

type namespaceConnWrapper struct {
	net.Conn

	parent *namespaceConnector
}

func (nc namespaceConnWrapper) Close() error {
	defer func() {
		if nc.parent.deref() {
			if nc.parent.autoclean {
				ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
				defer done()

				slog.Info("[namespace] destroying instance", "instance_id", nc.parent.md.InstanceId)

				// Missing a context.
				if _, err := nc.parent.cli.Compute.DestroyInstance(ctx, &computepb.DestroyInstanceRequest{
					InstanceId: nc.parent.md.InstanceId,
				}); err != nil {
					slog.Error("[namespace] destroy failed", "error", err)
				}
			}
		}
	}()

	return nc.Conn.Close()
}

func maybeSuffix(prefix, suffix string) string {
	if suffix != "" {
		return fmt.Sprintf("%s-%s", prefix, suffix)
	}

	return prefix
}

func parseShape(shape string) (int32, int32, error) {
	parts := strings.Split(shape, "x")
	if len(parts) == 2 {
		cpu, err1 := strconv.ParseInt(parts[0], 10, 32)
		mem, err2 := strconv.ParseInt(parts[1], 10, 32)
		if err := multierr.New(err1, err2); err != nil {
			return 0, 0, fmt.Errorf("failed to parse shape: %w", err)
		}

		return int32(cpu), int32(mem), nil
	}

	return 0, 0, errors.New("unexpected shape, expected {cpu}x{mem}")
}
