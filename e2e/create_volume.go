package e2e

import (
	"fmt"
	"os"
	"text/template"
)

func main() {
	// Create a template for the `volume.yaml` manifest.
	tmpl := template.Must(template.New("volume").Parse(`
kind: PersistentVolume
apiVersion: v1
metadata:
  name: my-volume
spec:
  capacity:
    storage: 1Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: my-storage-class
`))

	// Create a file to write the manifest to.
	f, err := os.Create("volume.yaml")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()

	// Write the manifest to the file.
	err = tmpl.Execute(f, nil)
	if err != nil {
		fmt.Println(err)
		return
	}

	// Apply the manifest.
	cmd := fmt.Sprintf("kubectl apply -f volume.yaml")
	err = os.Run(cmd)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Manifest created and applied successfully.")
}
