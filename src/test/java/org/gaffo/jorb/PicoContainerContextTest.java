package org.gaffo.jorb;

import org.gaffo.jorb.pico.PicoContainerContext;
import org.junit.Before;
import org.junit.Test;

import static org.junit.Assert.assertEquals;

public class PicoContainerContextTest
{

    private PicoContainerContext out;

    @Before
    public void setup()
    {
        out = new PicoContainerContext();
    }

    @Test
    public void getProvider_returnsSameInstanceAsInitializedWith()
    {
        Integer expected = 3;
        out.register(expected);
        assertEquals(expected, out.get(Integer.class));
    }

    @Test
    public void getProvider_UnknownObject_returnsCreated()
    {
        assertEquals(null, out.get(String.class));
    }

    @Test
    public void getProvider_defaultCtorClass_returnsClass()
    {
        out.register(PicoContainerContextTest.class);
        assertEquals(new PicoContainerContextTest(), out.get(PicoContainerContextTest.class));
    }

    @Override
    public boolean equals(Object o)
    {
        if (this == o) return true;
        if (o == null || getClass() != o.getClass()) return false;

        PicoContainerContextTest that = (PicoContainerContextTest) o;

        if (out != null ? !out.equals(that.out) : that.out != null) return false;

        return true;
    }

    @Override
    public int hashCode()
    {
        return out != null ? out.hashCode() : 0;
    }
}
