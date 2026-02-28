import { GoogleGenAI } from "@google/genai";

const ai = new GoogleGenAI({ apiKey: process.env.GEMINI_API_KEY });

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

export async function analyzeStock(ticker: string, position?: string): Promise<TechnicalAnalysis> {
  const prompt = `
    You are a senior quantitative trader at Citadel who combines technical analysis with statistical models to time entries and exits.
    Analyze the stock ticker: ${ticker}. ${position ? `Current position: ${position}` : "No current position."}

    Provide a full technical analysis breakdown including:
    1. Current trend direction on daily, weekly, and monthly timeframes.
    2. Key support and resistance levels with exact price points.
    3. Moving average analysis (50-day, 100-day, 200-day) and crossover signals.
    4. RSI, MACD, and Bollinger Band readings with plain-English interpretation.
    5. Volume trend analysis and what it signals about buyer vs seller strength.
    6. Chart pattern identification (head and shoulders, cup and handle, etc.).
    7. Fibonacci retracement levels for potential bounce zones.
    8. Ideal entry price, stop-loss level, and profit target.
    9. Risk-to-reward ratio for the current setup.
    10. Confidence rating: strong buy, buy, neutral, sell, strong sell.

    Format the response as a structured report card with a clear trade plan summary.
    Use Markdown for the report card.
    
    Return the final result in JSON format with the following structure:
    {
      "ticker": "${ticker}",
      "reportCard": "Markdown string of the full analysis",
      "tradePlan": {
        "entry": "Price",
        "stopLoss": "Price",
        "profitTarget": "Price",
        "riskReward": "Ratio",
        "confidence": "Rating"
      },
      "summary": "One sentence summary of the outlook"
    }
  `;

  const response = await ai.models.generateContent({
    model: "gemini-3.1-pro-preview",
    contents: prompt,
    config: {
      responseMimeType: "application/json",
      tools: [{ googleSearch: {} }],
    },
  });

  try {
    const data = JSON.parse(response.text || "{}");
    return data;
  } catch (error) {
    console.error("Failed to parse Gemini response:", error);
    throw new Error("Failed to analyze stock. Please try again.");
  }
}
