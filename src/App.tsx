import React, { useEffect, useState } from "react";
const CURRENCIES = [
  { code: "USD", symbol: "$", label: "US Dollar" },
  { code: "EUR", symbol: "€", label: "Euro" },
  { code: "GBP", symbol: "£", label: "British Pound" },
  { code: "INR", symbol: "₹", label: "Indian Rupee" },
  { code: "JPY", symbol: "¥", label: "Japanese Yen" },
  // Add more as needed
];
import { 
  TrendingUp, 
  TrendingDown, 
  Search, 
  Shield, 
  Target, 
  AlertTriangle, 
  BarChart3, 
  Activity, 
  ChevronRight,
  Loader2,
  Cpu
} from "lucide-react";
import { motion, AnimatePresence } from "motion/react";
import Markdown from "react-markdown";
import { 
  LineChart, 
  Line, 
  XAxis, 
  YAxis, 
  CartesianGrid, 
  Tooltip, 
  ResponsiveContainer,
  AreaChart,
  Area
} from "recharts";
import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import { analyzeStockNvidia, type TechnicalAnalysis } from "./services/nvidiaService";

function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// Simulated data for the chart background
const generateSimulatedData = () => {
  const data = [];
  let price = 150;
  for (let i = 0; i < 30; i++) {
    price = price + (Math.random() - 0.5) * 5;
    data.push({ name: i, price: parseFloat(price.toFixed(2)) });
  }
  return data;
};

const encryptedApiStorageKey = "citadel:nvidia-api-key:enc";
const keyDbName = "citadel-secure-store";
const keyStoreName = "crypto";
const keyId = "nvidia-api-key";

function openKeyDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(keyDbName, 1);
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(keyStoreName)) {
        request.result.createObjectStore(keyStoreName);
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

function getKeyFromDb(db: IDBDatabase): Promise<CryptoKey | undefined> {
  return new Promise((resolve, reject) => {
    const tx = db.transaction(keyStoreName, "readonly");
    const store = tx.objectStore(keyStoreName);
    const request = store.get(keyId);
    request.onsuccess = () => resolve(request.result as CryptoKey | undefined);
    request.onerror = () => reject(request.error);
  });
}

function putKeyInDb(db: IDBDatabase, key: CryptoKey): Promise<void> {
  return new Promise((resolve, reject) => {
    const tx = db.transaction(keyStoreName, "readwrite");
    const store = tx.objectStore(keyStoreName);
    const request = store.put(key, keyId);
    request.onsuccess = () => resolve();
    request.onerror = () => reject(request.error);
  });
}

async function getOrCreateStorageKey(): Promise<CryptoKey> {
  const db = await openKeyDb();
  const existing = await getKeyFromDb(db);
  if (existing) return existing;

  const generated = await crypto.subtle.generateKey(
    { name: "AES-GCM", length: 256 },
    false,
    ["encrypt", "decrypt"]
  );
  await putKeyInDb(db, generated);
  return generated;
}

async function encryptApiKey(value: string): Promise<string> {
  const key = await getOrCreateStorageKey();
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const encoded = new TextEncoder().encode(value);
  const encrypted = await crypto.subtle.encrypt({ name: "AES-GCM", iv }, key, encoded);
  return JSON.stringify({
    iv: Array.from(iv),
    data: Array.from(new Uint8Array(encrypted)),
  });
}

async function decryptApiKey(payload: string): Promise<string> {
  const parsed = JSON.parse(payload) as { iv: number[]; data: number[] };
  const key = await getOrCreateStorageKey();
  const decrypted = await crypto.subtle.decrypt(
    { name: "AES-GCM", iv: new Uint8Array(parsed.iv) },
    key,
    new Uint8Array(parsed.data)
  );
  return new TextDecoder().decode(decrypted);
}

export default function App() {
  const [ticker, setTicker] = useState("");
  const [position, setPosition] = useState("");
  const [currency, setCurrency] = useState("USD");
  const [apiKey, setApiKey] = useState("");
  const [apiKeySaved, setApiKeySaved] = useState(false);
  const [loading, setLoading] = useState(false);
  const [analysis, setAnalysis] = useState<TechnicalAnalysis | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [chartData] = useState(generateSimulatedData());
  const [savedApiKey, setSavedApiKey] = useState("");

  useEffect(() => {
    let mounted = true;
    const loadStoredKey = async () => {
      try {
        const encrypted = localStorage.getItem(encryptedApiStorageKey);
        if (!encrypted) return;
        const decrypted = await decryptApiKey(encrypted);
        if (!mounted) return;
        setApiKey(decrypted);
        setSavedApiKey(decrypted);
        setApiKeySaved(true);
      } catch {
        localStorage.removeItem(encryptedApiStorageKey);
      }
    };
    loadStoredKey();
    return () => {
      mounted = false;
    };
  }, []);

  const handleSaveApiKey = async () => {
    const trimmed = apiKey.trim();
    if (!trimmed) {
      localStorage.removeItem(encryptedApiStorageKey);
      setApiKeySaved(false);
      setSavedApiKey("");
      return;
    }
    try {
      const encrypted = await encryptApiKey(trimmed);
      localStorage.setItem(encryptedApiStorageKey, encrypted);
      setApiKey(trimmed);
      setSavedApiKey(trimmed);
      setApiKeySaved(true);
    } catch {
      setError("Unable to save API key securely in this browser.");
    }
  };

  const handleClearApiKey = () => {
    localStorage.removeItem(encryptedApiStorageKey);
    setApiKey("");
    setApiKeySaved(false);
    setSavedApiKey("");
  };

  const handleAnalyze = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!ticker) return;

    setLoading(true);
    setError(null);
    try {
      const result = await analyzeStockNvidia(ticker, position, currency, apiKey.trim() || undefined);
      setAnalysis(result);
    } catch (err: any) {
      setError(err.message || "An unexpected error occurred.");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen citadel-grid flex flex-col">
      {/* Header */}
      <header className="border-b border-white/10 bg-black/50 backdrop-blur-md sticky top-0 z-50">
        <div className="max-w-7xl mx-auto px-4 h-16 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 bg-white flex items-center justify-center rounded-lg">
              <Shield className="text-black w-6 h-6" />
            </div>
            <div>
              <h1 className="font-mono text-lg font-bold tracking-tighter uppercase">The Citadel</h1>
              <p className="text-[10px] text-gray-500 font-mono uppercase tracking-widest">Quantitative Technical Analysis</p>
            </div>
          </div>
          <div className="flex items-center gap-6 text-xs font-mono uppercase tracking-widest text-gray-400">
            <div className="flex items-center gap-2">
              <div className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
              Live Terminal
            </div>
            <span>System v4.2.0</span>
            <div className="flex items-center gap-2 ml-6">
              <label htmlFor="currency-select" className="font-mono text-xs text-gray-400">Currency:</label>
              <select
                id="currency-select"
                value={currency}
                onChange={e => setCurrency(e.target.value)}
                className="bg-black/70 border border-white/10 rounded px-2 py-1 text-xs text-white font-mono"
              >
                {CURRENCIES.map(c => (
                  <option key={c.code} value={c.code}>{c.label} ({c.code})</option>
                ))}
              </select>
            </div>
          </div>
        </div>
      </header>

      <main className="flex-1 max-w-7xl mx-auto w-full px-4 py-8">
        {/* Input Section */}
        <section className="mb-12">
          <div className="max-w-2xl mx-auto">
            <h2 className="text-4xl font-bold text-center mb-2 tracking-tight">Technical Breakdown</h2>
            <p className="text-gray-500 text-center mb-8">Enter ticker symbol for institutional-grade quantitative analysis.</p>
            
            <form onSubmit={handleAnalyze} className="space-y-4">
              <div className="relative">
                <Search className="absolute left-4 top-1/2 -translate-y-1/2 text-gray-500 w-5 h-5" />
                <input
                  type="text"
                  placeholder="TICKER (e.g. NVDA, TSLA, BTC)"
                  className="w-full bg-white/5 border border-white/10 rounded-xl py-4 pl-12 pr-4 focus:outline-none focus:ring-2 focus:ring-white/20 transition-all font-mono text-xl uppercase placeholder:text-gray-700"
                  value={ticker}
                  onChange={(e) => setTicker(e.target.value)}
                />
              </div>
              <div className="flex gap-4">
                <input
                  type="text"
                  placeholder="CURRENT POSITION (OPTIONAL)"
                  className="flex-1 bg-white/5 border border-white/10 rounded-xl py-3 px-4 focus:outline-none focus:ring-2 focus:ring-white/20 transition-all font-mono text-sm placeholder:text-gray-700"
                  value={position}
                  onChange={(e) => setPosition(e.target.value)}
                />
                <button
                  type="submit"
                  disabled={loading || !ticker}
                  className="bg-white text-black font-bold px-8 rounded-xl hover:bg-gray-200 transition-colors disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2"
                >
                  {loading ? <Loader2 className="w-5 h-5 animate-spin" /> : <Cpu className="w-5 h-5" />}
                  {loading ? "Analyzing..." : "Execute"}
                </button>
              </div>
              <div className="bg-white/5 border border-white/10 rounded-xl p-4 space-y-3">
                <div className="flex items-center justify-between">
                  <p className="text-xs font-mono uppercase tracking-widest text-gray-400">NVIDIA API Key</p>
                  <span className={cn("text-[10px] font-mono uppercase tracking-widest", apiKeySaved ? "text-emerald-400" : "text-gray-500")}>
                    {apiKeySaved ? "Saved in browser" : "Not saved"}
                  </span>
                </div>
                <input
                  type="password"
                  placeholder="Enter your NVIDIA API key"
                  className="w-full bg-black/40 border border-white/10 rounded-lg py-2.5 px-3 focus:outline-none focus:ring-2 focus:ring-white/20 transition-all font-mono text-xs"
                  value={apiKey}
                  onChange={(e) => {
                    const value = e.target.value;
                    setApiKey(value);
                    setApiKeySaved(value.trim() === savedApiKey.trim() && !!savedApiKey);
                  }}
                  autoComplete="off"
                />
                <div className="flex gap-2">
                  <button
                    type="button"
                    onClick={handleSaveApiKey}
                    className="px-3 py-2 rounded-lg bg-white text-black text-xs font-bold uppercase tracking-wider hover:bg-gray-200 transition-colors"
                  >
                    Save API Key
                  </button>
                  <button
                    type="button"
                    onClick={handleClearApiKey}
                    className="px-3 py-2 rounded-lg border border-white/20 text-xs font-bold uppercase tracking-wider hover:bg-white/10 transition-colors"
                  >
                    Clear
                  </button>
                </div>
              </div>
            </form>
          </div>
        </section>

        {/* Error Message */}
        <AnimatePresence>
          {error && (
            <motion.div
              initial={{ opacity: 0, y: -20 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -20 }}
              className="max-w-2xl mx-auto mb-8 p-4 bg-red-500/10 border border-red-500/20 rounded-xl flex items-center gap-3 text-red-400"
            >
              <AlertTriangle className="w-5 h-5" />
              <p className="text-sm">{error}</p>
            </motion.div>
          )}
        </AnimatePresence>

        {/* Results Section */}
        <AnimatePresence mode="wait">
          {analysis ? (
            <motion.div
              key="analysis"
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              className="grid grid-cols-1 lg:grid-cols-3 gap-8"
            >
              {/* Left Column: Trade Plan & Stats */}
              <div className="space-y-6">
                {/* Trade Plan Card */}
                <div className="bg-white/5 border border-white/10 rounded-2xl p-6 overflow-hidden relative">
                  <div className="absolute top-0 right-0 p-4 opacity-10">
                    <Target className="w-24 h-24" />
                  </div>
                  <h3 className="text-xs font-mono uppercase tracking-widest text-gray-500 mb-4 flex items-center gap-2">
                    <Target className="w-3 h-3" />
                    Trade Plan Summary
                  </h3>
                  
                  <div className="space-y-4 relative z-10">
                    <div className="flex justify-between items-end">
                      <span className="text-gray-500 text-xs font-mono uppercase">Confidence</span>
                      <span className={cn(
                        "text-lg font-bold uppercase",
                        analysis.tradePlan.confidence.includes("Buy") ? "text-emerald-400" : 
                        analysis.tradePlan.confidence.includes("Sell") ? "text-red-400" : "text-yellow-400"
                      )}>
                        {analysis.tradePlan.confidence}
                      </span>
                    </div>
                    
                    <div className="h-px bg-white/10" />
                    
                    <div className="grid grid-cols-2 gap-4">
                      <div>
                        <p className="text-gray-500 text-[10px] font-mono uppercase mb-1">Entry Price</p>
                        <p className="text-xl font-bold font-mono">{analysis.tradePlan.entry}</p>
                      </div>
                      <div>
                        <p className="text-gray-500 text-[10px] font-mono uppercase mb-1">Profit Target</p>
                        <p className="text-xl font-bold font-mono text-emerald-400">{analysis.tradePlan.profitTarget}</p>
                      </div>
                      <div>
                        <p className="text-gray-500 text-[10px] font-mono uppercase mb-1">Stop Loss</p>
                        <p className="text-xl font-bold font-mono text-red-400">{analysis.tradePlan.stopLoss}</p>
                      </div>
                      <div>
                        <p className="text-gray-500 text-[10px] font-mono uppercase mb-1">Risk/Reward</p>
                        <p className="text-xl font-bold font-mono">{analysis.tradePlan.riskReward}</p>
                      </div>
                    </div>
                  </div>
                </div>

                {/* Market Outlook */}
                <div className="bg-white/5 border border-white/10 rounded-2xl p-6">
                  <h3 className="text-xs font-mono uppercase tracking-widest text-gray-500 mb-4 flex items-center gap-2">
                    <Activity className="w-3 h-3" />
                    Executive Summary
                  </h3>
                  <p className="text-sm text-gray-300 italic leading-relaxed">
                    "{analysis.summary}"
                  </p>
                </div>

                {/* Visual Chart (Simulated) */}
                <div className="bg-white/5 border border-white/10 rounded-2xl p-6 h-64">
                  <h3 className="text-xs font-mono uppercase tracking-widest text-gray-500 mb-4 flex items-center gap-2">
                    <BarChart3 className="w-3 h-3" />
                    Recent Momentum
                  </h3>
                  <div className="h-40 w-full">
                    <ResponsiveContainer width="100%" height="100%">
                      <AreaChart data={chartData}>
                        <defs>
                          <linearGradient id="colorPrice" x1="0" y1="0" x2="0" y2="1">
                            <stop offset="5%" stopColor="#ffffff" stopOpacity={0.1}/>
                            <stop offset="95%" stopColor="#ffffff" stopOpacity={0}/>
                          </linearGradient>
                        </defs>
                        <Area type="monotone" dataKey="price" stroke="#ffffff" fillOpacity={1} fill="url(#colorPrice)" strokeWidth={2} />
                      </AreaChart>
                    </ResponsiveContainer>
                  </div>
                  <div className="mt-4 flex justify-between text-[10px] font-mono text-gray-600 uppercase">
                    <span>30D Trend</span>
                    <span>Volatility: High</span>
                  </div>
                </div>
              </div>

              {/* Right Column: Full Report Card */}
              <div className="lg:col-span-2">
                <div className="bg-white/5 border border-white/10 rounded-2xl p-8">
                  <div className="flex items-center justify-between mb-8 border-b border-white/10 pb-4">
                    <div className="flex items-center gap-4">
                      <div className="w-12 h-12 bg-white/10 rounded-xl flex items-center justify-center">
                        <BarChart3 className="text-white w-6 h-6" />
                      </div>
                      <div>
                        <h2 className="text-2xl font-bold uppercase tracking-tighter">{analysis.ticker}</h2>
                        <p className="text-xs text-gray-500 font-mono uppercase">Full Technical Report Card</p>
                      </div>
                    </div>
                    <div className="text-right">
                      <p className="text-[10px] text-gray-500 font-mono uppercase">Report ID</p>
                      <p className="text-xs font-mono">CIT-{Math.random().toString(36).substring(7).toUpperCase()}</p>
                    </div>
                  </div>

                  <div className="markdown-body">
                    <Markdown>{analysis.reportCard}</Markdown>
                  </div>
                </div>
              </div>
            </motion.div>
          ) : loading ? (
            <motion.div
              key="loading"
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              className="flex flex-col items-center justify-center py-24 space-y-6"
            >
              <div className="relative">
                <div className="w-24 h-24 border-4 border-white/5 border-t-white rounded-full animate-spin" />
                <Shield className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-8 h-8 text-white/20" />
              </div>
              <div className="text-center space-y-2">
                <p className="text-xl font-bold tracking-tight">Accessing Market Data...</p>
                <p className="text-gray-500 font-mono text-xs uppercase animate-pulse">Running quantitative models & technical indicators</p>
              </div>
            </motion.div>
          ) : (
            <motion.div
              key="empty"
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              className="flex flex-col items-center justify-center py-24 opacity-20 grayscale"
            >
              <BarChart3 className="w-32 h-32 mb-4" />
              <p className="text-sm font-mono uppercase tracking-widest">Awaiting Input Ticker</p>
            </motion.div>
          )}
        </AnimatePresence>
      </main>

      {/* Footer */}
      <footer className="border-t border-white/10 py-8 mt-auto">
        <div className="max-w-7xl mx-auto px-4 flex flex-col md:flex-row justify-between items-center gap-4">
          <div className="flex items-center gap-2 opacity-50">
            <Shield className="w-4 h-4" />
            <span className="text-[10px] font-mono uppercase tracking-widest">Citadel Quantitative Systems</span>
          </div>
          <p className="text-[10px] text-gray-600 font-mono uppercase tracking-widest text-center">
            For informational purposes only. Not financial advice. Past performance is not indicative of future results.
          </p>
          <div className="flex gap-4 opacity-50">
            <TrendingUp className="w-4 h-4" />
            <TrendingDown className="w-4 h-4" />
          </div>
        </div>
      </footer>
    </div>
  );
}
