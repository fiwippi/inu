# inu
Content-addressed filesharing system which optimises to reduce IXP congestion.

## FAQ

**Q: What's the point?**

- Internet exchanges are consistently reaching new peaks of traffic flow
- This occurs when a group of clients all request data from a similar set of origin servers
- Could we instruct clients which are already in the same Autonomous System to share the data between themselves if possible?

Yes, read the [paper](paper.pdf).

**Q: Does it work?**

Yes, inu is fault-tolerant, scalable, and, congestion-avoidant (for IXPs).

An evaluation is performed in the [paper](paper.pdf).

**Q: Is this production-ready?**

No!

**Q: Can I still use this to share files?**

Well yeah, but, the filesharing component of the system is not especially ergonomic. Consider using good ol' BitTorrent
if you're looking for something similar.

**Q: Wait... so, is inu useful at all?**

Yes, as a research idea, not as a fully fledged practical solution.

inu shines due to its approach to routing, which leverages existing best-effort infrastructure to be simple, effective
and maintainable. These capabilities can be integrated into existing systems to enhance their performance.
