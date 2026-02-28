

export interface TechnicalAnalysis {
  ticker: string;
  reportCard: string;
  tradePlan: {
    entry: string;
    stopLoss: string;
    profitTarget: string;
    riskReward: string;
    confidence: "Strong Buy" | "Buy" | "Neutral" | "Sell" | "Strong Sell";
  };
  summary: string;
}

export async function analyzeStockNvidia(ticker: string, position?: string, currency: string = "USD"): Promise<TechnicalAnalysis> {
  const response = await fetch("/api/analyze-nvidia", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ticker, position, currency }),
  });
  if (!response.ok) {
    const error = await response.json();
    throw new Error(error.error || "Failed to analyze stock with NVIDIA.");
  }
  return response.json();
}
