package main

import (
	"flag"
	"io/ioutil"
	"os"
	"path"

	"github.com/BurntSushi/toml"
	"github.com/buildpack/packs"

	"github.com/buildpack/lifecycle"
)

var (
	buildpackPath string
	groupPath     string
	infoPath      string
	metadataPath  string
)

func init() {
	packs.InputBPPath(&buildpackPath)
	packs.InputBPGroupPath(&groupPath)
	packs.InputDetectInfoPath(&infoPath)

	packs.InputMetadataPath(&metadataPath)
}

func main() {
	flag.Parse()
	if flag.NArg() != 0 || groupPath == "" || infoPath == "" || metadataPath == "" {
		packs.Exit(packs.FailCode(packs.CodeInvalidArgs, "parse arguments"))
	}
	packs.Exit(build())
}

func build() error {
	buildpacks, err := lifecycle.NewBuildpackMap(buildpackPath)
	if err != nil {
		return packs.FailErr(err, "read buildpack directory")
	}
	var group lifecycle.BuildpackGroup
	if _, err := toml.DecodeFile(groupPath, &group); err != nil {
		return packs.FailErr(err, "read group")
	}
	group.Buildpacks = buildpacks.MapIn(group.Buildpacks)

	info, err := ioutil.ReadFile(infoPath)
	if err != nil {
		return packs.FailErr(err, "read detect info")
	}
	builder := &lifecycle.Builder{
		PlatformDir: lifecycle.DefaultPlatformDir,
		Buildpacks:  group.Buildpacks,
		In:          info,
		Out:         os.Stdout,
		Err:         os.Stderr,
	}
	env := &lifecycle.Env{
		Getenv:  os.Getenv,
		Setenv:  os.Setenv,
		Environ: os.Environ,
		Map:     lifecycle.POSIXBuildEnv,
	}
	metadata, err := builder.Build(
		lifecycle.DefaultAppDir,
		lifecycle.DefaultCacheDir,
		lifecycle.DefaultLaunchDir,
		env,
	)
	if err != nil {
		return packs.FailErrCode(err, packs.CodeFailedBuild)
	}
	if err := os.MkdirAll(path.Dir(metadataPath), 0750); err != nil {
		return packs.FailErr(err, "create metadata dir")
	}
	mdFile, err := os.Create(metadataPath)
	if err != nil {
		return packs.FailErr(err, "create metadata file")
	}
	defer mdFile.Close()
	if err := toml.NewEncoder(mdFile).Encode(metadata); err != nil {
		return packs.FailErr(err, "write metadata")
	}
	return nil
}
