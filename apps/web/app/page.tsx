import { redirect } from "next/navigation";
import { getAccessToken } from "@/lib/session";

/** The landing route sends signed-in users to their dashboard and everyone
 * else to login. The middleware guards the dashboard itself. */
export default async function Home() {
  const token = await getAccessToken();
  redirect(token ? "/dashboard" : "/login");
}
