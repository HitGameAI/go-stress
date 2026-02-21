/*
 * @Author: kamalyes 501893067@qq.com
 * @Date: 2026-01-09 00:00:00
 * @LastEditors: kamalyes 501893067@qq.com
 * @LastEditTime: 2026-01-09 11:15:00
 * @FilePath: \go-stress\statistics\report_html.go
 * @Description: 简化的HTML报告模板
 *
 * Copyright (c) 2025 by kamalyes, All Rights Reserved.
 */
package statistics

const reportHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Go-Stress {{if .IsRealtime}}实时{{end}}性能测试报告</title>
    <script src="https://cdn.jsdelivr.net/npm/echarts@5.4.3/dist/echarts.min.js"></script>
    <link rel="stylesheet" href="report.css">
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>⚡ Go-Stress {{if .IsRealtime}}实时{{end}}性能测试报告</h1>
            {{if .IsRealtime}}
            <div style="display: flex; align-items: center; gap: 15px;">
                <div class="status-badge">
                    <div class="status-dot" id="statusDot"></div>
                    <span id="statusText">实时监控中</span>
                </div>
                <button id="pauseBtn" onclick="togglePause()" style="padding: 10px 20px; background: #ffc107; color: #333; border: none; border-radius: 6px; cursor: pointer; font-weight: bold; transition: all 0.3s;">
                    ⏸ 暂停
                </button>
                <button id="stopBtn" onclick="stopMonitoring()" style="padding: 10px 20px; background: #dc3545; color: white; border: none; border-radius: 6px; cursor: pointer; font-weight: bold; transition: all 0.3s;">
                    ⏹ 停止
                </button>
            </div>
            {{else}}
            <div class="status-badge">
                <div class="status-dot"></div>
                <span>实时监控中</span>
            </div>
            {{end}}
        </div>
        
        {{if not .IsRealtime}}
        <!-- 文件加载器 -->
        <div class="file-loader" id="fileLoader">
            <h3>📂 加载测试报告数据</h3>
            <p>请选择对应的 JSON 数据文件</p>
            <p style="font-size: 0.9em; opacity: 0.8; margin-top: -10px;">💡 提示: 请选择同目录下的 <strong>{{.JSONFilename}}</strong></p>
            <label class="file-input-wrapper">
                <input type="file" id="jsonFileInput" accept=".json" onchange="handleFileSelect(event)">
                选择 JSON 文件
            </label>
            <div class="file-name" id="fileName"></div>
        </div>
        {{end}}
        
        <!-- 配置信息区域 -->
        <div class="config-info" id="configInfo" style="display: none;">
            <h3>⚙️ 配置信息</h3>
            <div class="config-grid">
                <div class="config-item">
                    <span class="config-label">协议:</span>
                    <span class="config-value" id="config-protocol">-</span>
                </div>
                <div class="config-item">
                    <span class="config-label">并发数:</span>
                    <span class="config-value" id="config-concurrency">-</span>
                </div>
                <div class="config-item">
                    <span class="config-label">计划请求数:</span>
                    <span class="config-value" id="config-total-reqs">-</span>
                </div>
            </div>
        </div>
        
        <!-- 统一的指标卡片（实时和静态都使用） -->
        <div class="metrics-grid" id="metricsGrid" {{if not .IsRealtime}}style="display: none;"{{end}}>
            <div class="metric-card">
                <div class="metric-label">总请求数</div>
                <div class="metric-value" id="total-requests">0</div>
            </div>
            <div class="metric-card">
                <div class="metric-label">成功请求</div>
                <div class="metric-value success" id="success-requests">0</div>
            </div>
            <div class="metric-card">
                <div class="metric-label">失败请求</div>
                <div class="metric-value error" id="failed-requests">0</div>
            </div>
            <div class="metric-card">
                <div class="metric-label">跳过请求</div>
                <div class="metric-value" style="color: #ffc107;" id="skipped-requests">0</div>
            </div>
            <div class="metric-card">
                <div class="metric-label">成功率</div>
                <div class="metric-value" id="success-rate">0%</div>
            </div>
            <div class="metric-card">
                <div class="metric-label">QPS</div>
                <div class="metric-value" id="qps">0</div>
            </div>
            <div class="metric-card">
                <div class="metric-label">平均响应时间</div>
                <div class="metric-value" id="avg-duration">0ms</div>
            </div>
            <div class="metric-card">
                <div class="metric-label">最小响应时间</div>
                <div class="metric-value" id="min-duration">0ms</div>
            </div>
            <div class="metric-card">
                <div class="metric-label">最大响应时间</div>
                <div class="metric-value" id="max-duration">0ms</div>
            </div>
            <div class="metric-card">
                <div class="metric-label">P50 (中位数)</div>
                <div class="metric-value" id="p50">0ms</div>
            </div>
            <div class="metric-card">
                <div class="metric-label">P90</div>
                <div class="metric-value" id="p90">0ms</div>
            </div>
            <div class="metric-card">
                <div class="metric-label">P95</div>
                <div class="metric-value" id="p95">0ms</div>
            </div>
            <div class="metric-card">
                <div class="metric-label">P99</div>
                <div class="metric-value" id="p99">0ms</div>
            </div>
            {{if .IsRealtime}}
            <div class="metric-card">
                <div class="metric-label">运行时间</div>
                <div class="metric-value" id="elapsed">0s</div>
            </div>
            {{else}}
            <div class="metric-card">
                <div class="metric-label">测试时长</div>
                <div class="metric-value" id="test-duration">0s</div>
            </div>
            {{end}}
        </div>
                
        <div class="content">
            <div class="section">
                <div class="section-title">📈 实时图表</div>
                <div class="chart-container">
                    <div id="durationChart" class="chart"></div>
                </div>
                <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 20px;">
                    <div class="chart-container">
                        <div id="statusChart" class="chart"></div>
                    </div>
                    <div class="chart-container">
                        <div id="errorChart" class="chart"></div>
                    </div>
                </div>
            </div>
            
            <div class="section" id="apiSection" style="display: none;">
                <div class="section-title">
                    <span>🧩 API 汇总</span>
                </div>
                <div style="overflow-x: auto;">
                    <table>
                        <thead>
                            <tr>
                                <th>API 名称</th>
                                <th>请求数</th>
                                <th>成功</th>
                                <th>失败</th>
                                <th>跳过</th>
                                <th>成功率</th>
                                <th>平均</th>
                                <th>P50</th>
                                <th>P95</th>
                                <th>P99</th>
                                <th>QPS</th>
                            </tr>
                        </thead>
                        <tbody id="api-tbody"></tbody>
                    </table>
                </div>
            </div>

            <div class="section">
                <div class="section-title">
                    <span>📋 请求明细</span>
                </div>
                
                <!-- 高级筛选栏 -->
                <div style="padding: 20px; background: #f8f9fa; border-radius: 8px; margin-bottom: 30px; position: relative;">
                    <div style="display: grid; grid-template-columns: 2fr 1fr 1fr 1fr auto; gap: 15px; align-items: center;">
                        <input type="text" id="searchPath" placeholder="搜索 URL 路径..." style="padding: 10px; border: 1px solid #ddd; border-radius: 4px; font-size: 14px;" onkeyup="filterDetails()">
                        
                        <select id="methodFilter" onchange="filterDetails()" style="padding: 10px; border: 1px solid #ddd; border-radius: 4px; font-size: 14px;">
                            <option value="">所有方法</option>
                            <option value="GET">GET</option>
                            <option value="POST">POST</option>
                            <option value="PUT">PUT</option>
                            <option value="DELETE">DELETE</option>
                            <option value="PATCH">PATCH</option>
                        </select>
                        
                        <select id="statusFilter" onchange="filterDetails()" style="padding: 10px; border: 1px solid #ddd; border-radius: 4px; font-size: 14px;">
                            <option value="">所有状态码</option>
                            <option value="2xx">2xx 成功</option>
                            <option value="3xx">3xx 重定向</option>
                            <option value="4xx">4xx 客户端错误</option>
                            <option value="5xx">5xx 服务端错误</option>
                        </select>
                        
                        <select id="durationFilter" onchange="filterDetails()" style="padding: 10px; border: 1px solid #ddd; border-radius: 4px; font-size: 14px;">
                            <option value="">所有响应时间</option>
                            <option value="<100">&lt; 100ms</option>
                            <option value="100-500">100-500ms</option>
                            <option value="500-1000">500-1000ms</option>
                            <option value=">1000">&gt; 1000ms</option>
                        </select>
                        
                        <button onclick="clearFilters()" style="padding: 10px 20px; background: #6c757d; color: white; border: none; border-radius: 4px; cursor: pointer; white-space: nowrap;">清除筛选</button>
                    </div>
                </div>
                
                <!-- Tab 切换 -->
                <div style="display: flex; gap: 10px; margin-bottom: 20px; border-bottom: 2px solid #e9ecef; background: white; position: relative;">
                    <button class="tab-btn active" onclick="switchTab('all')" id="tab-all">全部 (<span id="count-all">0</span>)</button>
                    <button class="tab-btn" onclick="switchTab('success')" id="tab-success">成功 (<span id="count-success">0</span>)</button>
                    <button class="tab-btn" onclick="switchTab('failed')" id="tab-failed">失败 (<span id="count-failed">0</span>)</button>
                    <button class="tab-btn" onclick="switchTab('skipped')" id="tab-skipped">跳过 (<span id="count-skipped">0</span>)</button>
                </div>
                
                <div style="overflow-x: auto;">
                    <table>
                        <thead>
                            <tr>
                                <th>ID</th>
                                <th>分组ID</th>
                                <th>API名称</th>
                                <th>时间</th>
                                <th>URL</th>
                                <th>方法</th>
                                <th>响应时间</th>
                                <th>状态码</th>
                                <th>状态</th>
                                <th>验证</th>
                                <th>大小</th>
                                <th>操作</th>
                            </tr>
                        </thead>
                        <tbody id="details-tbody">
                            <tr><td colspan="12" style="text-align:center;padding:40px;color:#6c757d;">加载中...</td></tr>
                        </tbody>
                    </table>
                    
                    <!-- 分页组件 -->
                    <div class="pagination" id="pagination" style="display: none;">
                        <button onclick="goToFirstPage()" id="firstBtn">首页</button>
                        <button onclick="previousPage()" id="prevBtn">上一页</button>
                        <span class="pagination-info">
                            第 <strong id="currentPage">1</strong> 页 / 共 <strong id="totalPages">1</strong> 页
                            (共 <strong id="totalRecords">0</strong> 条记录)
                        </span>
                        <button onclick="nextPage()" id="nextBtn">下一页</button>
                        <button onclick="goToLastPage()" id="lastBtn">末页</button>
                        <select id="pageSizeSelect" onchange="changePageSize()">
                            <option value="10">10条/页</option>
                            <option value="20" selected>20条/页</option>
                            <option value="50">50条/页</option>
                            <option value="100">100条/页</option>
                            <option value="200">200条/页</option>
                        </select>
                    </div>
                </div>
            </div>
        </div>
        
        <div class="footer">
            <p>由 Go-Stress 高性能压测工具生成 | © 2025 Kamalyes</p>
        </div>
    </div>
    
    <script src="report_actions.js"></script>
    <script src="report.js"></script>
</body>
</html>
`
