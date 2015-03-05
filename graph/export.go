package graph

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"path"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/docker/engine"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/registry"
)

// CmdImageExport exports all images with the given tag. All versions
// containing the same tag are exported. The resulting output is an
// uncompressed tar ball.
// name is the set of tags to export.
// out is the writer where the images are written to.
func (s *TagStore) CmdImageExport(job *engine.Job) engine.Status {
	if len(job.Args) < 1 {
		return job.Errorf("Usage: %s IMAGE [IMAGE...]\n", job.Name)
	}
	// get image json
	tempdir, err := ioutil.TempDir("", "docker-export-")
	if err != nil {
		return job.Error(err)
	}
	defer os.RemoveAll(tempdir)

	repoMap := map[string]Repository{}

	for _, name := range job.Args {
		name = registry.NormalizeLocalName(name)
		log.Debugf("Serializing %s", name)
		rootRepo := s.Repositories[name]
		if rootRepo != nil {
			// this is a base repo name, like 'busybox'
			for _, id := range rootRepo {
				if err := s.exportImage(job.Eng, id, tempdir, repoMap); err != nil {
					return job.Error(err)
				}
			}
		} else {
			// This is a named image like 'busybox:latest'
			img, err := s.LookupImage(name)
			if err != nil {
				return job.Error(err)
			}
			if img != nil {
				if err := s.exportImage(job.Eng, img.ID, tempdir, repoMap); err != nil {
					return job.Error(err)
				}

			} else {
				// this must be an ID that didn't get looked up just right?
				if err := s.exportImage(job.Eng, name, tempdir, repoMap); err != nil {
					return job.Error(err)
				}
			}
		}
		log.Debugf("End Serializing %s", name)
	}

	// write repositories, if there is something to write
	if len(repoMap) > 0 {
		repoJSON, _ := json.MarshalIndent(repoMap, "", "  ")
		if err := ioutil.WriteFile(path.Join(tempdir, "repositories"), repoJSON, os.FileMode(0644)); err != nil {
			return job.Error(err)
		}
	} else {
		log.Debugf("There were no repositories to write")
	}

	fs, err := archive.Tar(tempdir, archive.Uncompressed)
	if err != nil {
		return job.Error(err)
	}
	defer fs.Close()

	if _, err := io.Copy(job.Stdout, fs); err != nil {
		return job.Error(err)
	}
	log.Debugf("End export job: %s", job.Name)
	return engine.StatusOK
}

// FIXME: this should be a top-level function, not a class method
func (s *TagStore) exportImage(eng *engine.Engine, name, tempdir string, repoMap map[string]Repository) error {
	for n := name; n != ""; {
		// temporary directory
		tmpImageDir := path.Join(tempdir, n)
		if err := os.Mkdir(tmpImageDir, os.FileMode(0755)); err != nil {
			if os.IsExist(err) {
				return nil
			}
			return err
		}

		var version = "1.0"
		var versionBuf = []byte(version)

		if err := ioutil.WriteFile(path.Join(tmpImageDir, "VERSION"), versionBuf, os.FileMode(0644)); err != nil {
			return err
		}

		// serialize json
		json, err := os.Create(path.Join(tmpImageDir, "json"))
		if err != nil {
			return err
		}
		job := eng.Job("image_inspect", n)
		job.SetenvBool("raw", true)
		job.Stdout.Add(json)
		if err := job.Run(); err != nil {
			return err
		}

		// serialize filesystem
		fsTar, err := os.Create(path.Join(tmpImageDir, "layer.tar"))
		if err != nil {
			return err
		}
		job = eng.Job("image_tarlayer", n)
		job.Stdout.Add(fsTar)
		if err := job.Run(); err != nil {
			return err
		}

		log.Debugf("Serialised layer: %s", n)
		for repoName, repo := range s.Repositories {
			for tag, id := range repo {
				if id == n {
					if repo, ok := repoMap[repoName]; !ok {
						repoMap[repoName] = Repository{tag: id}
					} else {
						repo[tag] = id
					}
				}
			}
		}

		// find parent
		job = eng.Job("image_get", n)
		info, _ := job.Stdout.AddEnv()
		if err := job.Run(); err != nil {
			return err
		}
		n = info.Get("Parent")
	}
	return nil
}
