package file

import "testing"

func TestCloudStorageDriversEnforceBucketAndSafeKeyIsolation(t *testing.T) {
	t.Run("OSS rejects cross-bucket and traversal", func(t *testing.T) {
		svc := &ossFileService{bucketName: "tenant-main-bucket"}
		if _, _, key, err := svc.resolveOssTarget("oss://tenant-main-bucket/tenants/1/doc.pdf"); err != nil || key != "tenants/1/doc.pdf" {
			t.Fatalf("valid OSS path failed: key=%q err=%v", key, err)
		}
		if _, _, _, err := svc.resolveOssTarget("oss://foreign-bucket/tenants/1/doc.pdf"); err == nil {
			t.Fatal("expected cross-bucket OSS path to be rejected")
		}
		if _, _, _, err := svc.resolveOssTarget("oss://tenant-main-bucket/tenants/1/../../etc/passwd"); err == nil {
			t.Fatal("expected traversal OSS path to be rejected")
		}
	})

	t.Run("TOS rejects cross-bucket and traversal", func(t *testing.T) {
		svc := &tosFileService{bucketName: "tenant-tos-bucket"}
		if _, key, err := svc.resolveTOSTarget("tos://tenant-tos-bucket/tenants/1/doc.pdf"); err != nil || key != "tenants/1/doc.pdf" {
			t.Fatalf("valid TOS path failed: key=%q err=%v", key, err)
		}
		if _, _, err := svc.resolveTOSTarget("tos://foreign-bucket/tenants/1/doc.pdf"); err == nil {
			t.Fatal("expected cross-bucket TOS path to be rejected")
		}
		if _, _, err := svc.resolveTOSTarget("tos://tenant-tos-bucket/../secret.txt"); err == nil {
			t.Fatal("expected traversal TOS path to be rejected")
		}
	})

	t.Run("COS rejects cross-bucket and fallback bypass", func(t *testing.T) {
		svc := &cosFileService{bucketName: "tenant-cos-125000", region: "ap-shanghai"}
		if key, err := svc.parseCosObjectName("cos://tenant-cos-125000/ap-shanghai/tenants/1/doc.pdf"); err != nil || key != "tenants/1/doc.pdf" {
			t.Fatalf("valid COS path failed: key=%q err=%v", key, err)
		}
		if _, err := svc.parseCosObjectName("cos://other-cos-125000/ap-shanghai/tenants/1/doc.pdf"); err == nil {
			t.Fatal("expected cross-bucket COS path to be rejected")
		}
		if _, err := svc.parseCosObjectName("https://other-cos-125000.cos.ap-shanghai.myqcloud.com/tenants/1/doc.pdf"); err == nil {
			t.Fatal("expected cross-bucket HTTPS COS URL fallback to be rejected")
		}
	})

	t.Run("OBS rejects cross-bucket and traversal", func(t *testing.T) {
		svc := &obsFileService{bucketName: "tenant-obs-bucket"}
		if key, err := svc.parseObsFilePath("obs://tenant-obs-bucket/tenants/1/doc.pdf"); err != nil || key != "tenants/1/doc.pdf" {
			t.Fatalf("valid OBS path failed: key=%q err=%v", key, err)
		}
		if _, err := svc.parseObsFilePath("obs://other-obs-bucket/tenants/1/doc.pdf"); err == nil {
			t.Fatal("expected cross-bucket OBS path to be rejected")
		}
		if _, err := svc.parseObsFilePath("tenants/1/doc.pdf"); err == nil {
			t.Fatal("expected bare non-obs scheme path to be rejected")
		}
	})
}
