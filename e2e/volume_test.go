package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	//. "github.com/onsi/gomega"
	"fmt"
	"flag"
	"context"
	 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	//corev1 "k8s.io/api/core/v1"
)

var _ = Describe("cephlet-volume", func() {
	/*It("Should pass", func() {
	  fmt.Println("Gingo working...........")
           Expect(1).To(Equal(1))
       })*/
	It("should create a basic volume", func(ctx SpecContext) {

		//todo
		//podList := corev1.PodList{}
		
		kubeconfig := flag.String("kubeconfig", "/home/ppanditrao/.kube/config", "location to your kubecofig file")
		config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err !=nil{
			//handle error
		}
		//fmt.Println(config)
		clientset, err :=kubernetes.NewForConfig(config)
		if err !=nil{
			//handle error
		}

		pods, err := clientset.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})
		if err !=nil{
			//handle error
		}
		fmt.Println(pods)
		
		//Expect(k8sClient.List(ctx, &podList)).To(Succeed())
		

	})
})

