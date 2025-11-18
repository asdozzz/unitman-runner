package unit

import (
	"crypto/md5"
	"fmt"
	"os"
	"runner/temporal/utils"
	"strings"
)

type Cache struct {
	ServiceName string
	Keys        []string
	Paths       []string
}

func makeSha1Sums(cache Cache, unitPath string) (bool, []string) {
	isValidKeys := true
	var sha1Sums []string
	for _, key := range cache.Keys {
		args := []string{"docker", "compose", "exec", "unit", "sh", "-c", "sha1sum " + key}
		sha1Sum, errCommand := utils.ExecCommand(unitPath, args)

		if errCommand != nil {
			fmt.Println("error copy cache key - " + key + ":")
			fmt.Println(errCommand)
			isValidKeys = false
			break
		} else {
			sha1Sums = append(sha1Sums, sha1Sum)
		}
	}

	return isValidKeys, sha1Sums
}

func RestoreImages(ProjectId string, UnitId string) {
	currentPath, err := os.Getwd()
	if err != nil {
		return
	}

	projectPath := currentPath + "/projects/" + ProjectId + "/"
	projectImagesPath := projectPath + "images/"

	unitPath := projectPath + "units/" + UnitId

	args := []string{"docker", "compose", "cp", projectImagesPath + ".", "unit:/podman_images/"}
	_, errCommand := utils.ExecCommand(unitPath, args)

	if errCommand != nil {
		fmt.Println("docker copy cache archive to container " + errCommand.Error())
		return
	}

	args = []string{"docker", "compose", "exec", "unit", "sh", "-c", "ls -1 /podman_images/*.tar | xargs --no-run-if-empty -L 1 podman load -i"}
	_, errCommand = utils.ExecCommand(unitPath, args)

	if errCommand != nil {
		fmt.Println("docker copy load images to container " + errCommand.Error())
		return
	}
}

func RestoreCache(ProjectName string, UnitName string, ProjectId string, UnitId string, caches []Cache) {
	err := os.Setenv("UNITMAN_PROJECT_NAME", ProjectName)

	if err != nil {
		fmt.Println("error set project name ")
		return
	}

	err = os.Setenv("UNITMAN_UNIT_NAME", UnitName)

	if err != nil {
		fmt.Println("error set project name ")
		return
	}

	currentPath, err := os.Getwd()
	if err != nil {
		return
	}

	projectPath := currentPath + "/projects/" + ProjectId + "/"
	projectCachePath := projectPath + "cache/"

	err = os.MkdirAll(projectCachePath, os.ModePerm)

	if err != nil {
		fmt.Println("error mkdir project cache dir " + projectCachePath)
		return
	}

	unitPath := projectPath + "units/" + UnitId

	for _, cache := range caches {
		isValidKeys, sha1Sums := makeSha1Sums(cache, unitPath)

		if !isValidKeys {
			break
		}
		//fmt.Println("sums:\n" + strings.Join(sha1Sums, "") + "###########")
		archiveName := makeSumFromString(strings.Join(sha1Sums, "")) + ".tar.gz"
		fmt.Println("archiveName:" + archiveName)

		archivePath := projectCachePath + "/" + cache.ServiceName + "/" + archiveName

		if _, err := os.Stat(archivePath); os.IsNotExist(err) {
			fmt.Printf("archive file %s not found", archiveName)
			break
		}

		args := []string{"docker", "compose", "cp", archivePath, "unit:/app/"}
		_, errCommand := utils.ExecCommand(unitPath, args)

		if errCommand != nil {
			fmt.Println("docker copy cache archive to container " + errCommand.Error())
			continue
		}

		args = []string{"docker", "compose", "exec", "unit", "sh", "-c", "tar -xzvf " + archiveName + " ."}
		_, errCommand = utils.ExecCommand(unitPath, args)

		if errCommand != nil {
			fmt.Println("unpack cache archive " + errCommand.Error())
			continue
		}

		args = []string{"docker", "compose", "exec", "unit", "sh", "-c", "rm -rf " + archiveName}
		_, errCommand = utils.ExecCommand(unitPath, args)

		if errCommand != nil {
			fmt.Println("unlink cache archive " + errCommand.Error())
			continue
		}
	}

	args := []string{"docker", "compose", "exec", "unit", "sh", "-c", "rm -rf cache"}
	_, errCommand := utils.ExecCommand(unitPath, args)

	if errCommand != nil {
		fmt.Println("docker error remove cache dir " + errCommand.Error())
	}
}

func makeSumFromString(sumFiles string) string {
	h := md5.Sum([]byte(sumFiles))
	return fmt.Sprintf("%x", h)
}

func SavePodmanImages(ProjectName string, ProjectId string, UnitId string) {
	currentPath, err := os.Getwd()
	if err != nil {
		return
	}
	projectPath := currentPath + "/projects/" + ProjectId + "/"
	projectImagesPath := projectPath + "images/"
	err = os.MkdirAll(projectImagesPath, os.ModePerm)

	if err != nil {
		fmt.Println("error mkdir project cache dir " + projectImagesPath)
		return
	}

	unitPath := projectPath + "units/" + UnitId

	args := []string{"docker", "compose", "exec", "unit", "sh", "-c", "./unitman_utils/bi.sh " + ProjectName}
	_, err = utils.ExecCommand(unitPath, args)

	if err != nil {
		fmt.Println("error:")
		fmt.Println(err)
		return
	}

	args = []string{"docker", "compose", "cp", "unit:/app/podman_images_backup/.", projectImagesPath}
	_, errCommand := utils.ExecCommand(unitPath, args)

	if errCommand != nil {
		fmt.Println("error copy images path - " + projectImagesPath + ":")
		fmt.Println(errCommand)
	}
}

func MakeCache(ProjectName string, UnitName string, ProjectId string, UnitId string, caches []Cache) {
	err := os.Setenv("UNITMAN_PROJECT_NAME", ProjectName)

	if err != nil {
		fmt.Println("error set project name ")
		return
	}

	err = os.Setenv("UNITMAN_UNIT_NAME", UnitName)

	if err != nil {
		fmt.Println("error set project name ")
		return
	}

	currentPath, err := os.Getwd()
	if err != nil {
		return
	}

	projectPath := currentPath + "/projects/" + ProjectId + "/"
	projectCachePath := projectPath + "cache/"
	err = os.MkdirAll(projectCachePath, os.ModePerm)

	if err != nil {
		fmt.Println("error mkdir project cache dir " + projectCachePath)
		return
	}

	unitPath := projectPath + "units/" + UnitId

	args := []string{"docker", "compose", "exec", "unit", "sh", "-c", "podman-compose ps --format='{{.ID}}###{{.Names}}'"}
	resultContainers, errCommand := utils.ExecCommand(unitPath, args)

	if errCommand != nil {
		fmt.Println("error:")
		fmt.Println(errCommand)
		return
	}

	containers := strings.Split(resultContainers, "\n")

	var results []string

	var validCaches []Cache

	for _, s := range containers {
		segments := strings.Split(s, "###")
		if len(segments) < 2 {
			continue
		}
		name := segments[1]
		id := segments[0]
		for _, cache := range caches {
			//fmt.Println(name + " contains: " + UnitName + "_" + ProjectName + "_" + cache.ServiceName)
			if !strings.Contains(name, UnitName+"_"+ProjectName+"_"+cache.ServiceName) {
				continue
			}

			results = append(results, id)

			contanerCacheDir := "unitman_cache/" + cache.ServiceName + "/"
			contanerCacheDirFiles := contanerCacheDir + "files/"

			args = []string{"docker", "compose", "exec", "unit", "sh", "-c", "mkdir -p " + contanerCacheDirFiles}
			_, errCommand = utils.ExecCommand(unitPath, args)

			if errCommand != nil {
				fmt.Println("error mkdir cache path " + contanerCacheDirFiles)
				fmt.Println(errCommand)
				continue
			}

			for _, path := range cache.Paths {
				args := []string{"docker", "compose", "exec", "unit", "sh", "-c", "podman cp " + id + ":" + path + " " + contanerCacheDirFiles + path}
				_, errCommand := utils.ExecCommand(unitPath, args)

				if errCommand != nil {
					fmt.Println("error copy cache path - " + path + ":")
					fmt.Println(errCommand)
					break
				}
			}

			validCaches = append(validCaches, cache)
		}
	}

	for _, cache := range validCaches {
		contanerCacheDir := "unitman_cache/" + cache.ServiceName + "/"
		projectServiceCachePath := projectCachePath + cache.ServiceName + "/"

		err = os.MkdirAll(projectServiceCachePath, os.ModePerm)

		if err != nil {
			fmt.Println("error mkdir project service cache dir " + projectServiceCachePath)
			continue
		}

		isValidKeys, sha1Sums := makeSha1Sums(cache, unitPath)

		if !isValidKeys {
			continue
		}

		archiveName := makeSumFromString(strings.Join(sha1Sums, "")) + ".tar.gz"

		cmdMakeArchive := "tar -czf " + archiveName + " --directory=" + contanerCacheDir + "files ."
		args = []string{"docker", "compose", "exec", "unit", "sh", "-c", cmdMakeArchive}
		_, errCommand = utils.ExecCommand(unitPath, args)

		if errCommand != nil {
			fmt.Println("error archive cache files")
			fmt.Println(errCommand)
			continue
		}

		args = []string{"docker", "compose", "cp", "-a", "unit:/app/" + archiveName, projectServiceCachePath}
		_, errCommand = utils.ExecCommand(unitPath, args)

		if errCommand != nil {
			fmt.Println("docker error copy cache archive " + errCommand.Error())
			continue
		}

		args = []string{"docker", "compose", "exec", "unit", "sh", "-c", "rm -rf " + archiveName}
		_, errCommand = utils.ExecCommand(unitPath, args)
	}
}
