package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"k8s.io/klog"
	"log"
	"math/big"
	"os"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kube-sidecar-injector/pkg"
)

func main() {
	// CA 配置
	subject := pkix.Name{
		Country:            []string{"CN"},
		Province:           []string{"Beijing"},
		Locality:           []string{"Beijing"},
		Organization:       []string{"admission.io"},
		OrganizationalUnit: []string{"admission.io"},
	}
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(2022),
		Subject:               subject,
		NotBefore:             time.Now(), // 有效期
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true, // 根证书
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	// 生成CA私钥
	caPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		klog.Error(err)
	}

	// 创建自签名的 CA 证书
	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		klog.Error(err)
	}

	// 编码证书文件
	caPEM := new(bytes.Buffer)
	if err := pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	}); err != nil {
		klog.Error(err)
	}

	dnsNames := []string{"kube-sidecar-injector",
		"kube-sidecar-injector.debug",
		"kube-sidecar-injector.debug.svc",
		"kube-sidecar-injector.debug.svc.cluster.local",
	}
	commonName := "kube-sidecar-injector.debug.svc"

	// kube-sidecar-injector.debug.svc
	// 服务端的证书配置
	subject.CommonName = commonName
	cert := &x509.Certificate{
		DNSNames:     dnsNames,
		SerialNumber: big.NewInt(2022),
		Subject:      subject,
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	// 生成服务端的私钥
	serverPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		klog.Error(err)
	}

	// 对服务端私钥签名
	serverCertBytes, err := x509.CreateCertificate(rand.Reader, cert, ca, &serverPrivKey.PublicKey, caPrivKey)
	if err != nil {
		klog.Error(err)
	}
	serverCertPEM := new(bytes.Buffer)
	if err := pem.Encode(serverCertPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverCertBytes,
	}); err != nil {
		klog.Error(err)
	}

	serverPrivKeyPEM := new(bytes.Buffer)
	if err := pem.Encode(serverPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(serverPrivKey),
	}); err != nil {
		klog.Error(err)
	}

	// 已经生成了CA server.pem server-key.pem

	if err := os.MkdirAll("/etc/webhook/certs/", 0666); err != nil {
		klog.Error(err)
	}

	if err := pkg.WriteFile("/etc/webhook/certs/tls.crt", serverCertPEM.Bytes()); err != nil {
		klog.Error(err)
	}

	if err := pkg.WriteFile("/etc/webhook/certs/tls.key", serverPrivKeyPEM.Bytes()); err != nil {
		klog.Error(err)
	}

	log.Println("webhook server tls generated successfully")

	if err := CreateAdmissionConfig(caPEM); err != nil {
		klog.Error(err)
	}

	klog.Info("webhook admission configuration object generated successfully")
}



func CreateAdmissionConfig(caCert *bytes.Buffer) error {
	clientset, err := pkg.InitKubernetesCli()
	if err != nil {
		return err
	}

	var (
		webhookNamespace, _ = os.LookupEnv("WEBHOOK_NAMESPACE")
		mutateCfgName, _    = os.LookupEnv("MUTATE_CONFIG")
		webhookService, _   = os.LookupEnv("WEBHOOK_SERVICE")
		mutatePath, _       = os.LookupEnv("MUTATE_PATH")
	)

	ctx := context.Background()

	if mutateCfgName != "" {
		// 创建 MutatingWebhookConfiguration
		mutateConfig := &admissionregistrationv1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name: mutateCfgName,
			},

			Webhooks: []admissionregistrationv1.MutatingWebhook{
				{
					Name: "kube-sidecar-injector.k8s.io",
					ClientConfig: admissionregistrationv1.WebhookClientConfig{
						CABundle: caCert.Bytes(),
						Service: &admissionregistrationv1.ServiceReference{
							Name:      webhookService,
							Namespace: webhookNamespace,
							Path:      &mutatePath,
						},
					},
					Rules: []admissionregistrationv1.RuleWithOperations{
						{
							Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create, admissionregistrationv1.Update},
							Rule: admissionregistrationv1.Rule{
								APIGroups:   []string{""},
								APIVersions: []string{"v1"},
								Resources:   []string{"pods"},
							},
						},
					},
					AdmissionReviewVersions: []string{"v1"},
					SideEffects: func() *admissionregistrationv1.SideEffectClass {
						se := admissionregistrationv1.SideEffectClassNone
						return &se
					}(),
				},
			},
		}
		mutateAdmissionClient := clientset.AdmissionregistrationV1().MutatingWebhookConfigurations()
		if _, err := mutateAdmissionClient.Get(ctx, mutateCfgName, metav1.GetOptions{}); err != nil {
			if errors.IsNotFound(err) {
				if _, err := mutateAdmissionClient.Create(ctx, mutateConfig, metav1.CreateOptions{}); err != nil {
					return err
				}
			} else {
				return err
			}
		} else {
			if _, err := mutateAdmissionClient.Update(ctx, mutateConfig, metav1.UpdateOptions{}); err != nil {
				return err
			}
		}
	}

	return nil
}
