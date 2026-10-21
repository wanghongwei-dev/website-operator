/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt" // 【新增】Status.URL 需要 fmt.Sprintf

	appsv1 "k8s.io/api/apps/v1"                   // 【新增】创建/更新 Deployment
	corev1 "k8s.io/api/core/v1"                   // 【新增】创建/更新 Service
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1" // 【新增】ObjectMeta、LabelSelector
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr" // 【新增】Service TargetPort
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil" // 【新增】CreateOrUpdate、SetControllerReference
	logf "sigs.k8s.io/controller-runtime/pkg/log"                  // 【保持不变】脚手架用别名 logf，代码统一用 logf.FromContext

	webv1 "github.com/wanghongwei-dev/website-operator/api/v1"
)

// WebSiteReconciler reconciles a WebSite object
type WebSiteReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=web.wanghongwei.dev,resources=websites,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=web.wanghongwei.dev,resources=websites/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=web.wanghongwei.dev,resources=websites/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete // 【新增】管理 Deployment 的权限
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete        // 【新增】管理 Service 的权限

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/reconcile
func (r *WebSiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// 【修改】脚手架原为 `_ = logf.FromContext(ctx)`，仅占位；
	// 这里改为赋值给 logger，供后续 logger.Error 使用
	logger := logf.FromContext(ctx)

	// 【新增】以下为完整调谐逻辑，替换脚手架原来的 `// TODO(user): your logic here`

	// 1. 获取当前 WebSite 资源
	var site webv1.WebSite
	if err := r.Get(ctx, req.NamespacedName, &site); err != nil {
		// 【新增】资源不存在时 IgnoreNotFound 返回 nil，直接结束本次调谐；
		// 其他错误原样返回，controller-runtime 会指数退避重试。
		// 注意：这里必须 return，不能吞掉错误继续执行，
		// 否则会用零值 site 创建空名的 Deployment/Service。
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. 确保 Deployment 存在且符合期望
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      site.Name + "-deployment",
			Namespace: site.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		// 【新增】防御性判断：CRD 已通过 +kubebuilder:default=1 设置默认值，
		// 此处兼容未经默认化的旧对象或直接构造的测试对象
		replicas := site.Spec.Replicas
		if replicas == 0 {
			replicas = 1
		}
		deploy.Spec.Replicas = &replicas

		// 【新增】Deployment.Spec.Selector 创建后不可变。
		// site.Name 本身不可变，恒定 selector 是安全的；
		// 仅在首次创建时赋值，避免未来 Spec 扩展后误改触发不可变错误。
		if deploy.CreationTimestamp.IsZero() {
			deploy.Spec.Selector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": site.Name},
			}
		}
		deploy.Spec.Template.ObjectMeta.Labels = map[string]string{"app": site.Name}
		deploy.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  "web",
			Image: site.Spec.Image,
			Ports: []corev1.ContainerPort{{ContainerPort: 80}},
		}}

		// 【新增】设置所有者引用，实现级联删除
		return controllerutil.SetControllerReference(&site, deploy, r.Scheme)
	})
	if err != nil {
		logger.Error(err, "failed to reconcile deployment")
		return ctrl.Result{}, err
	}

	// 3. 确保 Service 存在且符合期望
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      site.Name + "-service",
			Namespace: site.Namespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		// 【新增】注意：不要覆盖 svc.Spec.ClusterIP。
		// CreateOrUpdate 会先 Get，已存在时 ClusterIP 已被填充且不可变；
		// 新建时留空由 Kubernetes 自动分配。
		svc.Spec.Selector = map[string]string{"app": site.Name}
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       "http", // 多端口时必填，单端口也建议加上
			Protocol:   corev1.ProtocolTCP,
			Port:       80,
			TargetPort: intstr.FromInt32(80),
		}}
		svc.Spec.Type = corev1.ServiceTypeClusterIP

		return controllerutil.SetControllerReference(&site, svc, r.Scheme)
	})
	if err != nil {
		logger.Error(err, "failed to reconcile service")
		return ctrl.Result{}, err
	}

	// 4. 回写 Status
	// 【新增】新建 Deployment 时其 Status 为空，首次回写 ReadyReplicas 为 0；
	// Pod 就绪后 Deployment Status 变化，Owns(&appsv1.Deployment{}) 触发重新调谐，
	// 下一次 Reconcile 才读到正确值——这是正常的两阶段收敛。
	newURL := fmt.Sprintf("http://%s-service.%s.svc.cluster.local/", site.Name, site.Namespace)
	if site.Status.ReadyReplicas != deploy.Status.ReadyReplicas || site.Status.URL != newURL {
		site.Status.ReadyReplicas = deploy.Status.ReadyReplicas
		site.Status.URL = newURL
		if err := r.Status().Update(ctx, &site); err != nil {
			logger.Error(err, "failed to update status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebSiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webv1.WebSite{}).
		Named("website").
		Owns(&appsv1.Deployment{}). // 【新增】监听 owner 为 WebSite 的 Deployment，子资源变化/被删时重新入队
		Owns(&corev1.Service{}).    // 【新增】监听 owner 为 WebSite 的 Service
		Complete(r)
}
