import siteJson from "@/data/site.json";
import homeJson from "@/data/home.json";
import contactJson from "@/data/contact.json";
import type { SiteData, HomeData, ContactData } from "@/lib/types";

export const site = siteJson as SiteData;
export const home = homeJson as HomeData;
export const contact = contactJson as ContactData;
