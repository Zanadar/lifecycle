package lifecycle_test

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/buildpack/lifecycle"
	"github.com/buildpack/packs/img"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/sclevine/spec"
	"github.com/sclevine/spec/report"
)

func TestExporter(t *testing.T) {
	rand.Seed(time.Now().UTC().UnixNano())
	spec.Run(t, "Exporter", testExporter, spec.Report(report.Terminal{}))
}

func testExporter(t *testing.T, when spec.G, it spec.S) {
	var (
		exporter       *lifecycle.Exporter
		stdout, stderr *bytes.Buffer
		tmpDir         string
	)

	it.Before(func() {
		stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
		var err error
		tmpDir, err = ioutil.TempDir("", "pack.export.layer")
		if err != nil {
			t.Fatal(err)
		}
		exporter = &lifecycle.Exporter{
			TmpDir: tmpDir,
			Buildpacks: []lifecycle.Buildpack{
				{ID: "buildpack.id"},
			},
			Out: io.MultiWriter(stdout, it.Out()),
			Err: io.MultiWriter(stderr, it.Out()),
		}
	})
	it.After(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Fatal(err)
		}
	})

	when("#Export", func() {
		var stackImage v1.Image
		it.Before(func() {
			var err error
			stackImage, err = GetBusyboxWithEntrypoint()
			if err != nil {
				t.Fatalf("get busybox image for stack: %s", err)
			}
		})

		it("a simple launch dir exists", func() {
			image, err := exporter.Export("testdata/exporter/first/launch", stackImage, nil)
			if err != nil {
				t.Fatalf("Error: %s\n", err)
			}
			data, err := GetMetadata(image)
			if err != nil {
				t.Fatalf("Error: %s\n", err)
			}

			t.Log("adds buildpack metadata to label")
			if diff := cmp.Diff(data.Buildpacks[0].Key, "buildpack.id"); diff != "" {
				t.Fatal(diff)
			}

			t.Log("sets toml files in b metadata")
			if diff := cmp.Diff(data.Buildpacks[0].Layers["layer1"].Data, map[string]interface{}{"mykey": "myval"}); diff != "" {
				t.Fatalf(`Layer toml did not match: (-got +want)\n%s`, diff)
			}

			t.Log("adds app layer to image")
			if txt, err := GetImageFile(image, data.App.SHA, "launch/app/subdir/myfile.txt"); err != nil {
				t.Fatalf("Error: %s\n", err)
			} else if diff := cmp.Diff(strings.TrimSpace(txt), "mycontents"); diff != "" {
				t.Fatalf(`launch/app/subdir/myfile.txt: (-got +want)\n%s`, diff)
			}

			t.Log("adds buildpack/layer1 as layer")
			if txt, err := GetImageFile(image, data.Buildpacks[0].Layers["layer1"].SHA, "launch/buildpack.id/layer1/file-from-layer-1"); err != nil {
				t.Fatalf("Error: %s\n", err)
			} else if diff := cmp.Diff(strings.TrimSpace(txt), "echo text from layer 1"); diff != "" {
				t.Fatal("launch/buildpack.id/layer1/file-from-layer-1: (-got +want)", diff)
			}

			t.Log("adds buildpack/layer2 as layer")
			if txt, err := GetImageFile(image, data.Buildpacks[0].Layers["layer2"].SHA, "launch/buildpack.id/layer2/file-from-layer-2"); err != nil {
				t.Fatalf("Error: %s\n", err)
			} else if diff := cmp.Diff(strings.TrimSpace(txt), "echo text from layer 2"); diff != "" {
				t.Fatal("launch/buildpack.id/layer2/file-from-layer-2: (-got +want)", diff)
			}

			t.Log("Sets cmd from metadata.toml")
			cfg, err := image.ConfigFile()
			if err != nil {
				t.Fatal("reading image config")
			}
			if diff := cmp.Diff(cfg.Config.Cmd, []string{"./test_app.sh MyArg"}); diff != "" {
				t.Fatal(diff)
			}
		})

		when("rebuilding when toml exists without directory", func() {
			var firstImage v1.Image
			it.Before(func() {
				var err error
				firstImage, err = exporter.Export("testdata/exporter/first/launch", stackImage, nil)
				if err != nil {
					t.Fatalf("Error: %s\n", err)
				}
			})

			it("reuses layers if there is a layer.toml file", func() {
				image, err := exporter.Export("testdata/exporter/second/launch", stackImage, firstImage)
				if err != nil {
					t.Fatalf("Error: %s\n", err)
				}
				data, err := GetMetadata(image)
				if err != nil {
					t.Fatalf("Error: %s\n", err)
				}

				t.Log("sets toml files in image metadata")
				if diff := cmp.Diff(data.Buildpacks[0].Layers["layer1"].Data, map[string]interface{}{"mykey": "new val"}); diff != "" {
					t.Fatalf(`Layer toml did not match: (-got +want)\n%s`, diff)
				}

				t.Log("adds buildpack/layer1 as layer (from previous image)")
				if txt, err := GetImageFile(image, data.Buildpacks[0].Layers["layer1"].SHA, "launch/buildpack.id/layer1/file-from-layer-1"); err != nil {
					t.Fatalf("Error: %s\n", err)
				} else if diff := cmp.Diff(strings.TrimSpace(txt), "echo text from layer 1"); diff != "" {
					t.Fatal("launch/buildpack.id/layer1/file-from-layer-1: (-got +want)", diff)
				}

				t.Log("adds buildpack/layer2 as layer from directory")
				if txt, err := GetImageFile(image, data.Buildpacks[0].Layers["layer2"].SHA, "launch/buildpack.id/layer2/file-from-layer-2"); err != nil {
					t.Fatalf("Error: %s\n", err)
				} else if diff := cmp.Diff(strings.TrimSpace(txt), "echo text from new layer 2"); diff != "" {
					t.Fatal("launch/buildpack.id/layer2/file-from-layer-2: (-got +want)", diff)
				}
			})
		})
	}, spec.Parallel(), spec.Report(report.Terminal{}))
}

func GetBusyboxWithEntrypoint() (v1.Image, error) {
	stackStore, err := img.NewRegistry("busybox")
	if err != nil {
		return nil, fmt.Errorf("get store for busybox: %s", err)
	}
	stackImage, err := stackStore.Image()
	if err != nil {
		return nil, fmt.Errorf("get image for SCRATCH: %s", err)
	}
	configFile, err := stackImage.ConfigFile()
	if err != nil {
		return nil, err
	}
	config := *configFile.Config.DeepCopy()
	config.Entrypoint = []string{"sh", "-c"}
	return mutate.Config(stackImage, config)
}

func RandString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a' + byte(rand.Intn(26))
	}
	return string(b)
}

func GetLayerFile(layer v1.Layer, path string) (string, error) {
	r, err := layer.Uncompressed()
	if err != nil {
		return "", err
	}
	defer r.Close()
	tr := tar.NewReader(r)

	for {
		header, err := tr.Next()
		if err != nil {
			return "", err
		}

		if header.Name == path {
			buf, err := ioutil.ReadAll(tr)
			return string(buf), err
		}
	}
	return "", fmt.Errorf("file not found: %s", path)
}

func GetImageFile(image v1.Image, layerDigest, path string) (string, error) {
	hash, err := v1.NewHash(layerDigest)
	if err != nil {
		return "", err
	}
	layer, err := image.LayerByDiffID(hash)
	if err != nil {
		return "", err
	}
	return GetLayerFile(layer, path)
}

// func GetLayerFromImage(image v1.Image, keys ...string) {
// 	cfg, err := image.ConfigFile()
// 	if err != nil {
// 		t.Fatalf("Error: %s\n", err)
// 	}
// 	digest, err := jsonparser.GetString([]byte(cfg.Config.Labels["sh.packs.build"]), "app", "sha")
// 	if err != nil {
// 		t.Fatalf("Error: %s\n", err)
// 	}
// 	hash, err := v1.NewHash(digest)
// 	if err != nil {
// 		t.Fatalf("Error: %s\n", err)
// 	}
// 	layer, err := image.LayerByDiffID(hash)
// 	if err != nil {
// 		t.Fatalf("Error: %s\n", err)
// 	}
// }

type Metadata struct {
	Stack struct {
		SHA string `json:"sha"`
	} `json:"stack"`
	App struct {
		SHA string `json:"sha"`
	} `json:"app"`
	Buildpacks []struct {
		Key    string `json:"key"`
		Layers map[string]struct {
			SHA  string                 `json:"sha"`
			Data map[string]interface{} `json:"data"`
		} `json:"layers"`
	} `json:"buildpacks"`
}

func GetMetadata(image v1.Image) (Metadata, error) {
	var metadata Metadata
	cfg, err := image.ConfigFile()
	if err != nil {
		return metadata, fmt.Errorf("read config: %s", err)
	}
	label := cfg.Config.Labels["sh.packs.build"]
	if err := json.Unmarshal([]byte(label), &metadata); err != nil {
		return metadata, fmt.Errorf("unmarshal: %s", err)
	}
	return metadata, nil
}
