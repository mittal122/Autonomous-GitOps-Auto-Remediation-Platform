
# The Citadel Market Analysis AI

## Overview
The Citadel Market Analysis AI is a web application that provides institutional-grade, AI-powered technical analysis for stocks and cryptocurrencies. It leverages NVIDIA's advanced language models to generate detailed, structured technical reports and trade plans for any given ticker symbol.

## Features
- Enter any stock or crypto ticker (e.g., NVDA, TSLA, BTC) to receive a comprehensive technical analysis.
- AI-generated report includes:
  - Trend analysis (daily, weekly, monthly)
  - Support/resistance levels
  - Moving averages, RSI, MACD, Bollinger Bands
  - Volume and pattern analysis
  - Fibonacci retracement
  - Trade plan (entry, stop-loss, profit target, risk/reward, confidence)
  - Executive summary
- Clean, modern UI with simulated chart visuals.
- Powered by NVIDIA's AI API (securely called from a backend route).

## How It Works
1. User enters a ticker symbol and (optionally) a current position.
2. The frontend sends this data to a secure backend API route.
3. The backend calls NVIDIA's AI API with a detailed prompt.
4. The AI returns a structured JSON report, which is displayed in the UI.

## Example Usage
### Example 1
**Input:**
- Ticker: `AAPL`
- Position: *(left blank)*

**Output:**
- Full technical analysis report for Apple Inc., including trend, support/resistance, indicators, trade plan, and summary.

### Example 2
**Input:**
- Ticker: `BTC`
- Position: `Long from 42000`

**Output:**
- Technical analysis for Bitcoin, tailored to a long position from $42,000, with updated trade plan and risk/reward.

## Getting Started
1. **Clone the repository:**
   ```sh
   git clone https://github.com/your-username/The-Citadel-Market-analysis-ai.git
   cd The-Citadel-Market-analysis-ai
   ```
2. **Install dependencies:**
   ```sh
   npm install
   ```
3. **Set up environment variables:**
   - Copy `.env.example` to `.env` and add your NVIDIA API key:
     ```sh
     cp .env.example .env
     # Edit .env and set NVIDIA_API_KEY
     ```
4. **Run locally:**
   ```sh
   npm run dev
   ```
5. **Deploy:**
   - Deploy to Vercel or your preferred platform.
   - Set `NVIDIA_API_KEY` in your deployment environment variables.

## Security Note
Your NVIDIA API key is never exposed to the frontend. All AI requests are proxied through a secure backend route.

## License
MIT
