export type Adopter = {
  name: string;
  industry: string;
  useCases: string[];
  description: string;
  variant: "A" | "B";
  stage: "production" | "poc" | "ecosystem";
  logo: string;
  logoSize: { width: number; height: number };
  logoScale?: number;
  link?: { href: string; label: string };
};

export const adopterIssue = "";

// Enterprise industry scenarios and typical deployment patterns. See BRAND-ASSETS.md for asset details.
export const adopters: Adopter[] = [
  {
    name: "智能制造与供应链",
    industry: "工业制造",
    useCases: ["设备手册检索", "采购与 BOM 分析", "质检知识问答"],
    description: "聚合工艺规范、设备维护手册与多表采购台账，结合混合检索与表格数据分析辅助工程与供应链决策。",
    variant: "B",
    stage: "production",
    logo: "manufacturing.svg",
    logoSize: { width: 40, height: 40 },
  },
  {
    name: "医疗健康服务",
    industry: "医疗健康",
    useCases: ["员工知识助手", "合规文档问答"],
    description: "构建可追溯原文引用的临床指南、器械说明书与内部 SOP 知识库，服务于员工内部问答与专业咨询。",
    variant: "B",
    stage: "production",
    logo: "healthcare.svg",
    logoSize: { width: 40, height: 40 },
  },
  {
    name: "出版传媒与智库",
    industry: "出版传媒",
    useCases: ["专业文档分析", "自动 Wiki 沉淀"],
    description: "将海量行业研报、历史档案与图书稿件转化为可对话、可按主题图谱浏览的结构化知识资产。",
    variant: "B",
    stage: "production",
    logo: "publishing.svg",
    logoSize: { width: 40, height: 40 },
  },
  {
    name: "企业研发与 IT 运维",
    industry: "软件与互联网",
    useCases: ["架构知识库", "MCP 工具协同", "故障排查助手"],
    description: "同步 GitLab、Confluence 与飞书研发文档，通过 MCP 端点与受控沙箱赋能研发与 SRE 团队。",
    variant: "B",
    stage: "production",
    logo: "devops.svg",
    logoSize: { width: 40, height: 40 },
  },
  {
    name: "政企内网私有化",
    industry: "金融与政企",
    useCases: ["全离线部署", "多空间权限隔离", "审计合规"],
    description: "支持纯内网容器或 Kubernetes 集群部署，搭配本地大模型与细粒度空间权限管控，保障核心数据不出域。",
    variant: "B",
    stage: "production",
    logo: "private-cloud.svg",
    logoSize: { width: 40, height: 40 },
  },
  {
    name: "企业客户服务与工单",
    industry: "客户服务",
    useCases: ["智能客服接入", "FAQ 知识沉淀", "多渠道协同"],
    description: "连接企业微信、飞书、钉钉与网页嵌入组件，基于产品知识库与 FAQ 提供高准确率的一线坐席辅助与自助问答。",
    variant: "B",
    stage: "production",
    logo: "customer-support.svg",
    logoSize: { width: 40, height: 40 },
  },
];
