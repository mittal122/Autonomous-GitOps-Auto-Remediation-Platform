
import { NextApiRequest, NextApiResponse } from "next";
import OpenAI from "openai";

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== "POST") {
    return res.status(405).json({ error: "Method not allowed" });
  }

  const { ticker, position, currency } = req.body;
  const userApiKey = typeof req.body?.apiKey === "string" ? req.body.apiKey.trim() : "";
  const apiKey = userApiKey || (process.env.NVIDIA_API_KEY || "").trim();
  if (!ticker) {
    return res.status(400).json({ error: "Ticker is required" });
  }
  if (!apiKey) {
    return res.status(400).json({ error: "NVIDIA API key is required. Please enter your API key and try again." });
  }

  const prompt = `
    You are a senior quantitative trader at Citadel who combines technical analysis with statistical models to time entries and exits.
    Analyze the stock ticker: ${ticker}. ${position ? `Current position: ${position}` : "No current position."}

    All price values, levels, and trade plan numbers must be in ${currency || "USD"}. Use the correct currency symbol and format for all numbers. If the ticker is a cryptocurrency, use the selected currency for all fiat conversions.

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

  try {
    const client = new OpenAI({
      baseURL: "https://integrate.api.nvidia.com/v1",
      apiKey,
    });

    const completion = await client.chat.completions.create({
      model: "openai/gpt-oss-120b",
      messages: [{ role: "user", content: prompt }],
      temperature: 1,
      top_p: 1,
      max_tokens: 4096,
      stream: false,
    });
    let text = completion.choices?.[0]?.message?.content || "";
    // Extract JSON from code block or extra text
    const match = text.match(/```(?:json)?([\s\S]*?)```/i);
    if (match) {
      text = match[1];
    }
    // Fallback: try to find first { ... } block
    if (!match) {
      const curly = text.indexOf('{');
      if (curly !== -1) {
        text = text.slice(curly, text.lastIndexOf('}') + 1);
      }
    }
    const data = JSON.parse(text);
    res.status(200).json(data);
  } catch (error: any) {
    res.status(500).json({ error: error.message || "Failed to analyze stock with NVIDIA." });
  }
}
