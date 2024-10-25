# Kubelet Validation

# The regular expression adds validation for kubelet.kubeReserved and kubelet.systemReserved values of the map are resource.Quantity
# Quantity: https://github.com/kubernetes/apimachinery/blob/d82afe1e363acae0e8c0953b1bc230d65fdb50e2/pkg/api/resource/quantity.go#L100
# EC2NodeClass Validation:
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.kubeletConfiguration.properties.kubeReserved.additionalProperties.pattern = "^(\+|-)?(([0-9]+(\.[0-9]*)?)|(\.[0-9]+))(([KMGTPE]i)|[numkMGTPE]|([eE](\+|-)?(([0-9]+(\.[0-9]*)?)|(\.[0-9]+))))?$"' -i config/components/crds/karpenter.k8s.alicloud_ecsnodeclasses.yaml
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.kubeletConfiguration.properties.systemReserved.additionalProperties.pattern = "^(\+|-)?(([0-9]+(\.[0-9]*)?)|(\.[0-9]+))(([KMGTPE]i)|[numkMGTPE]|([eE](\+|-)?(([0-9]+(\.[0-9]*)?)|(\.[0-9]+))))?$"' -i config/components/crds/karpenter.k8s.alicloud_ecsnodeclasses.yaml

# The regular expression is a validation for kubelet.evictionHard and kubelet.evictionSoft are percentage or a resource.Quantity
# Quantity: https://github.com/kubernetes/apimachinery/blob/d82afe1e363acae0e8c0953b1bc230d65fdb50e2/pkg/api/resource/quantity.go#L100
# EC2NodeClass Validation:
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.kubeletConfiguration.properties.evictionHard.additionalProperties.pattern = "^((\d{1,2}(\.\d{1,2})?|100(\.0{1,2})?)%||(\+|-)?(([0-9]+(\.[0-9]*)?)|(\.[0-9]+))(([KMGTPE]i)|[numkMGTPE]|([eE](\+|-)?(([0-9]+(\.[0-9]*)?)|(\.[0-9]+))))?)$"' -i config/components/crds/karpenter.k8s.alicloud_ecsnodeclasses.yaml
yq eval '.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.kubeletConfiguration.properties.evictionSoft.additionalProperties.pattern = "^((\d{1,2}(\.\d{1,2})?|100(\.0{1,2})?)%||(\+|-)?(([0-9]+(\.[0-9]*)?)|(\.[0-9]+))(([KMGTPE]i)|[numkMGTPE]|([eE](\+|-)?(([0-9]+(\.[0-9]*)?)|(\.[0-9]+))))?)$"' -i config/components/crds/karpenter.k8s.alicloud_ecsnodeclasses.yaml
