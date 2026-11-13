App({
  onLaunch() {
    const settings = wx.getStorageSync("genyee_settings");
    if (!settings) {
      wx.setStorageSync("genyee_settings", {
        baseUrl: "http://localhost:8080",
        apiKey: "",
        selectedKnowledgeBaseId: "",
        locale: "zh"
      });
    }
  }
});
