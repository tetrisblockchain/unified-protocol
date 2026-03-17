export default async function handler(req, res) {
  const { query } = req.query;

  // The Architect's Node RPC Endpoint (Replace with your actual Node IP/URL)
  const NODE_RPC_URL = process.env.NODE_RPC_URL || "http://your-node-ip:8545";

  if (!query) {
    return res.status(400).json({ error: "Missing search query" });
  }

  try {
    const response = await fetch(NODE_RPC_URL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        jsonrpc: "2.0",
        method: "ufi_getSearchData",
        params: [query],
        id: 1,
      }),
    });

    const data = await response.json();
    
    // Return the blockchain results to the frontend
    return res.status(200).json(data.result || []);
  } catch (error) {
    console.error("RPC Error:", error);
    return res.status(500).json({ error: "Failed to connect to UniFied Node" });
  }
}